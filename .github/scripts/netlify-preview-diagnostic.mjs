import assert from "node:assert/strict";
import { appendFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const NETLIFY_API_ORIGIN = "https://api.netlify.com";
const NETLIFY_ACCESS_ORIGIN = "https://app.netlify.com";
const GITHUB_API_ORIGIN = "https://api.github.com";
const LOG_SOCKET_URL = "wss://socketeer.services.netlify.com/build/logs";
const MAX_ARTIFACT_BYTES = 1024 * 1024;
const MAX_HTTP_BYTES = 256 * 1024;
const MAX_RECORD_BYTES = 16 * 1024;
const LOG_WINDOW_MS = 90_000;
const artifactPath = join(process.env.RUNNER_TEMP || "/tmp", "netlify-preview-diagnostic.jsonl");
const secretValues = new Set(
  Object.entries(process.env)
    .filter(([name, value]) => /TOKEN|SECRET|PASSWORD|CREDENTIAL|AUTH|PRIVATE_KEY|ACCESS_KEY/i.test(name) && value.length >= 8)
    .map(([, value]) => value),
);
let artifactBytes = 0;
let artifactFull = false;

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function redactText(value, additionalSecrets = []) {
  let text = String(value ?? "");
  const secrets = [...secretValues, ...additionalSecrets].filter((secret) => secret.length >= 8);
  secrets.sort((a, b) => b.length - a.length);
  for (const secret of secrets) {
    text = text.replace(new RegExp(escapeRegExp(secret), "g"), "[REDACTED]");
  }
  return text
    .replace(/\bBearer\s+[^\s,;"']+/gi, "Bearer [REDACTED]")
    .replace(/\b((?:NETLIFY|GITHUB|GH|ACTIONS)[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|CREDENTIAL|KEY))(\s*[:=]\s*)[^\s,;"']+/gi, "$1$2[REDACTED]")
    .replace(/\b((?:authorization|proxy-authorization|x-api-key|x-auth-token|cookie|set-cookie))(\s*[:=]\s*)[^\r\n]+/gi, "$1$2[REDACTED]")
    .replace(/\b((?:access[_-]?token|refresh[_-]?token|id[_-]?token|client[_-]?secret|api[_-]?key|password|passwd|secret|credential|private[_-]?key))(\s*[:=]\s*)[^\s,;"']+/gi, "$1$2[REDACTED]")
    .replace(/([?&](?:access_token|token|signature|sig|auth|x-amz-signature|x-amz-credential|x-amz-security-token)=)[^&#\s]+/gi, "$1[REDACTED]")
    .replace(/(https?:\/\/)[^/@\s]+:[^/@\s]+@/gi, "$1[REDACTED]@")
    .replace(/\bgh[pousr]_[A-Za-z0-9_]{20,}\b/g, "[REDACTED]")
    .replace(/\bgithub_pat_[A-Za-z0-9_]{20,}\b/g, "[REDACTED]")
    .replace(/\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b/g, "[REDACTED]")
    .replace(/\bAKIA[0-9A-Z]{16}\b/g, "[REDACTED]");
}

function stripControlCharacters(text) {
  return [...text].map((character) => {
    const code = character.codePointAt(0);
    if ((code < 32 && code !== 9 && code !== 10 && code !== 13) || code === 127) return "?";
    return character;
  }).join("");
}

function safeValue(value, depth = 0) {
  if (typeof value === "string") {
    return stripControlCharacters(redactText(value)).slice(0, 5000);
  }
  if (typeof value === "number" || typeof value === "boolean" || value === null) {
    return value;
  }
  if (Array.isArray(value)) {
    return value.slice(0, 20).map((item) => safeValue(item, depth + 1));
  }
  if (typeof value === "object" && depth < 3) {
    return Object.fromEntries(
      Object.entries(value).slice(0, 30).map(([key, item]) => [redactText(key).slice(0, 100), safeValue(item, depth + 1)]),
    );
  }
  return "[omitted]";
}

function appendRecord(record) {
  if (artifactFull) return false;
  let line = `${JSON.stringify(safeValue(record))}\n`;
  let bytes = Buffer.byteLength(line);
  if (bytes > MAX_RECORD_BYTES) {
    line = `${JSON.stringify({ event: "record_truncated", message: redactText(record.message ?? record.event ?? "record exceeded limit").slice(0, 1000) })}\n`;
    bytes = Buffer.byteLength(line);
  }
  if (artifactBytes + bytes > MAX_ARTIFACT_BYTES) {
    const marker = `${JSON.stringify({ event: "capture_limit", max_bytes: MAX_ARTIFACT_BYTES })}\n`;
    const markerBytes = Buffer.byteLength(marker);
    if (artifactBytes + markerBytes <= MAX_ARTIFACT_BYTES) {
      appendFileSync(artifactPath, marker, { encoding: "utf8" });
      process.stdout.write(marker);
      artifactBytes += markerBytes;
    }
    artifactFull = true;
    return false;
  }
  appendFileSync(artifactPath, line, { encoding: "utf8" });
  process.stdout.write(line);
  artifactBytes += bytes;
  return true;
}

function extractErrorFields(value) {
  if (!value || typeof value !== "object") return {};
  const result = {};
  for (const key of ["error_message", "failure_message", "failure_reason", "error_code", "error_type", "message", "code", "status", "stage"]) {
    if (typeof value[key] === "string" || typeof value[key] === "number") result[key] = value[key];
  }
  if (typeof value.error === "string") result.error = value.error;
  if (value.error && typeof value.error === "object") {
    for (const key of ["message", "code", "type", "stage", "status"]) {
      if (typeof value.error[key] === "string" || typeof value.error[key] === "number") result[`error_${key}`] = value.error[key];
    }
  }
  return result;
}

function metadataSummary(value) {
  if (!value || typeof value !== "object") return {};
  const result = {};
  for (const key of ["id", "site_id", "build_id", "commit_ref", "state", "context", "branch", "name"]) {
    if (typeof value[key] === "string" || typeof value[key] === "number") result[key] = value[key];
  }
  return Object.assign(result, extractErrorFields(value));
}

function hasDirectCause(value) {
  if (!value || typeof value !== "object") return false;
  return ["error_message", "failure_message", "failure_reason", "error_code", "error_type"].some(
    (key) => value[key] !== undefined && value[key] !== null && String(value[key]).trim() !== "",
  ) || (typeof value.error === "string" && value.error.trim() !== "") || Boolean(
    value.error && typeof value.error === "object" &&
      ["message", "code", "type"].some((key) => value.error[key] !== undefined && value.error[key] !== null && String(value.error[key]).trim() !== ""),
  );
}

async function readBoundedBody(response, maxBytes = MAX_HTTP_BYTES) {
  const declaredLength = Number(response.headers.get("content-length"));
  if (Number.isFinite(declaredLength) && declaredLength > maxBytes) {
    await response.body?.cancel();
    throw new Error(`response body exceeds ${maxBytes} byte metadata limit`);
  }
  if (!response.body) return "";
  const reader = response.body.getReader();
  const chunks = [];
  let total = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      await reader.cancel();
      throw new Error(`response body exceeds ${maxBytes} byte metadata limit`);
    }
    chunks.push(Buffer.from(value));
  }
  return Buffer.concat(chunks, total).toString("utf8");
}

async function requestJson(label, url, allowedOrigin, bearerToken = "") {
  const parsed = new URL(url);
  if (parsed.origin !== allowedOrigin) throw new Error(`${label} URL origin was not allowlisted`);
  const headers = { accept: "application/json" };
  if (bearerToken) headers.authorization = `Bearer ${bearerToken}`;
  const response = await fetch(parsed, {
    method: "GET",
    headers,
    redirect: "manual",
    signal: AbortSignal.timeout(15_000),
  });
  if (response.status >= 300 && response.status < 400) {
    await response.body?.cancel();
    appendRecord({ event: "http_response", request: label, status: response.status, redirect: "blocked; credentials not forwarded" });
    throw new Error(`${label} returned HTTP ${response.status} redirect; refusing to forward credentials`);
  }
  let bodyText;
  try {
    bodyText = await readBoundedBody(response);
  } catch (error) {
    appendRecord({ event: "http_response", request: label, status: response.status, error: error.message });
    throw error;
  }
  let data;
  try {
    data = JSON.parse(bodyText);
  } catch {
    appendRecord({
      event: "http_response",
      request: label,
      status: response.status,
      error: response.ok ? "response was not valid JSON; body omitted" : "non-JSON error body omitted",
      body_bytes: Buffer.byteLength(bodyText),
    });
    throw Object.assign(
      new Error(`${label} returned HTTP ${response.status} with non-JSON response`),
      { httpStatus: response.status },
    );
  }
  appendRecord({ event: "http_response", request: label, status: response.status, fields: extractErrorFields(data) });
  if (!response.ok) {
    const fields = extractErrorFields(data);
    throw Object.assign(
      new Error(`${label} returned HTTP ${response.status}${fields.message ? `: ${fields.message}` : ""}`),
      { httpStatus: response.status, responseData: data },
    );
  }
  return data;
}

function requireEqual(label, actual, expected) {
  if (actual !== expected) {
    appendRecord({ event: "preflight_mismatch", field: label, actual, expected });
    throw new Error(`preflight ${label} mismatch`);
  }
}

function websocketCapture(deployId, siteId, accessToken) {
  return new Promise((resolve, reject) => {
    const Socket = globalThis.WebSocket;
    let socket;
    let timer;
    let finished = false;
    let logMessages = 0;
    let messageChain = Promise.resolve();
    const startedAt = Date.now();
    const done = (error = null) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      messageChain.finally(() => error ? reject(error) : resolve({ logMessages, elapsed_ms: Date.now() - startedAt }));
    };
    try {
      socket = new Socket(LOG_SOCKET_URL);
    } catch (error) {
      reject(new Error(`cannot create Netlify log WebSocket: ${redactText(error.message)}`));
      return;
    }
    timer = setTimeout(() => {
      appendRecord({ event: "websocket_window_elapsed", window_ms: LOG_WINDOW_MS, log_messages: logMessages });
      socket.close(1000, "diagnostic window complete");
      done();
    }, LOG_WINDOW_MS);
    socket.addEventListener("open", () => {
      if (socket.url !== LOG_SOCKET_URL) {
        appendRecord({ event: "websocket_redirect_blocked", expected_url: LOG_SOCKET_URL, actual_url: socket.url });
        socket.close(1008, "unexpected log socket URL");
        done(new Error("Netlify log WebSocket URL changed during connection; token was not sent"));
        return;
      }
      appendRecord({ event: "websocket_open", endpoint: LOG_SOCKET_URL });
      try {
        socket.send(JSON.stringify({ deploy_id: deployId, site_id: siteId, access_token: accessToken }));
      } catch (error) {
        appendRecord({ event: "websocket_send_error", message: error?.message ?? "unknown socket send failure" });
        socket.close(1011, "log request could not be sent");
        done(error);
      }
    });
    socket.addEventListener("message", (event) => {
      messageChain = messageChain.then(async () => {
        if (artifactFull) {
          socket.close(1000, "capture size limit reached");
          return;
        }
        let raw;
        if (typeof event.data === "string") raw = event.data;
        else if (event.data && typeof event.data.text === "function") {
          if (event.data.size > MAX_ARTIFACT_BYTES) {
            appendRecord({ event: "websocket_message_omitted", reason: "message exceeded capture limit" });
            socket.close(1009, "message too large");
            return;
          }
          raw = await event.data.text();
        } else {
          appendRecord({ event: "websocket_message_omitted", reason: "unsupported message type" });
          return;
        }
        if (Buffer.byteLength(raw) > MAX_ARTIFACT_BYTES) {
          appendRecord({ event: "websocket_message_omitted", reason: "message exceeded capture limit" });
          socket.close(1009, "message too large");
          return;
        }
        let message;
        try {
          message = JSON.parse(raw);
        } catch {
          appendRecord({ event: "websocket_message_parse_error", body_bytes: Buffer.byteLength(raw), message: "payload omitted" });
          return;
        }
        const safeMessage = typeof message.message === "string" ? message.message : "";
        const fields = {
          event: "deploy_log",
          timestamp: message.ts ?? "",
          type: message.type ?? "log",
          level: message.level ?? "",
          stage: message.stage ?? message.phase ?? "",
          status: message.status ?? "",
          code: message.code ?? "",
          message: safeMessage,
        };
        appendRecord(fields);
        logMessages += 1;
        if (message.type === "error") appendRecord({ event: "deploy_log_error", ...extractErrorFields(message) });
        if (artifactFull) socket.close(1000, "capture size limit reached");
      }).catch((error) => {
        appendRecord({ event: "websocket_message_error", message: error.message });
        socket.close(1011, "message processing failed");
      });
    });
    socket.addEventListener("error", () => {
      appendRecord({ event: "websocket_error", message: "WebSocket transport error; details omitted" });
      socket.close();
    });
    socket.addEventListener("close", (event) => {
      appendRecord({ event: "websocket_closed", code: event.code, reason: event.reason, was_clean: event.wasClean });
      done();
    });
  });
}

async function main() {
  writeFileSync(artifactPath, "", { mode: 0o600 });
  artifactBytes = 0;
  appendRecord({ event: "diagnostic_start", node_version: process.versions.node, websocket_available: typeof globalThis.WebSocket === "function" });

  const token = process.env.NETLIFY_AUTH_TOKEN;
  if (!token) {
    appendRecord({ event: "prerequisite", name: "NETLIFY_AUTH_TOKEN", status: "missing" });
    throw new Error("repository secret NETLIFY_AUTH_TOKEN is missing");
  }
  if (typeof globalThis.WebSocket !== "function") {
    throw new Error(`Node ${process.versions.node} does not provide global WebSocket; use a preinstalled Node runtime with WebSocket support`);
  }

  const deployId = process.env.NETLIFY_DEPLOY_ID;
  const buildId = process.env.NETLIFY_BUILD_ID;
  const siteId = process.env.NETLIFY_SITE_ID;
  const expectedCommit = process.env.EXPECTED_COMMIT;
  const expectedContext = process.env.EXPECTED_CONTEXT;
  const prNumber = process.env.EXPECTED_PR_NUMBER;
  const expectedSiteName = process.env.EXPECTED_SITE_NAME;

  const deploy = await requestJson(
    "deploy_metadata",
    `${NETLIFY_API_ORIGIN}/api/v1/deploys/${encodeURIComponent(deployId)}`,
    NETLIFY_API_ORIGIN,
    token,
  );
  const site = await requestJson(
    "site_metadata",
    `${NETLIFY_API_ORIGIN}/api/v1/sites/${encodeURIComponent(siteId)}`,
    NETLIFY_API_ORIGIN,
    token,
  );
  const review = await requestJson(
    "github_pull_request_preflight",
    `${GITHUB_API_ORIGIN}/repos/devsy-org/devsy/pulls/${encodeURIComponent(prNumber)}`,
    GITHUB_API_ORIGIN,
    process.env.GITHUB_TOKEN,
  );

  appendRecord({ event: "deployment_preflight", deploy: metadataSummary(deploy) });
  appendRecord({ event: "site_preflight", site: metadataSummary(site) });
  appendRecord({ event: "review_preflight", number: review.number, state: review.state, head_sha: review.head?.sha, head_ref: review.head?.ref });

  requireEqual("deploy_id", deploy.id, deployId);
  requireEqual("site_id", deploy.site_id, siteId);
  requireEqual("state", deploy.state, "error");
  requireEqual("commit_ref", deploy.commit_ref, expectedCommit);
  requireEqual("context", deploy.context, expectedContext);
  requireEqual("site.id", site.id, siteId);
  requireEqual("site.name", site.name, expectedSiteName);
  requireEqual("review.number", review.number, Number(prNumber));
  requireEqual("review.head.sha", review.head?.sha, expectedCommit);
  requireEqual("review.head.ref", review.head?.ref, deploy.branch);
  requireEqual("review.state", review.state, "open");
  requireEqual("build_id", deploy.build_id, buildId);

  let build = null;
  try {
    build = await requestJson(
      "build_metadata",
      `${NETLIFY_API_ORIGIN}/api/v1/builds/${encodeURIComponent(buildId)}`,
      NETLIFY_API_ORIGIN,
      token,
    );
    appendRecord({ event: "build_preflight", build: metadataSummary(build) });
    if (build.id !== undefined) requireEqual("build.id", build.id, buildId);
    if (build.site_id !== undefined) requireEqual("build.site_id", build.site_id, siteId);
    if (build.commit_ref !== undefined) requireEqual("build.commit_ref", build.commit_ref, expectedCommit);
  } catch (error) {
    if (hasDirectCause(deploy)) {
      appendRecord({ event: "direct_failure_cause", source: "deploy_metadata", fields: extractErrorFields(deploy) });
      appendRecord({ event: "diagnostic_complete", source: "deploy_metadata", deploy_state: deploy.state, build_metadata_status: error.httpStatus ?? "unavailable" });
      return;
    }
    if (error.httpStatus === 401 || error.httpStatus === 403) throw error;
    if (error.responseData && hasDirectCause(error.responseData)) {
      appendRecord({ event: "direct_failure_cause", source: "build_metadata", fields: extractErrorFields(error.responseData) });
      appendRecord({ event: "diagnostic_complete", source: "build_metadata", deploy_state: deploy.state, build_metadata_status: error.httpStatus });
      return;
    }
    appendRecord({ event: "build_metadata_unavailable", status: error.httpStatus ?? "network_or_body_error", message: error.message });
  }

  if (hasDirectCause(deploy) || (build && hasDirectCause(build))) {
    if (hasDirectCause(deploy)) appendRecord({ event: "direct_failure_cause", source: "deploy_metadata", fields: extractErrorFields(deploy) });
    if (build && hasDirectCause(build)) appendRecord({ event: "direct_failure_cause", source: "build_metadata", fields: extractErrorFields(build) });
    appendRecord({ event: "diagnostic_complete", source: "metadata", deploy_state: deploy.state, build_state: build?.state ?? "unavailable" });
    return;
  }

  const tokenUrl = new URL("/access-control/generate-access-control-token", NETLIFY_ACCESS_ORIGIN);
  tokenUrl.searchParams.set("deploy_id", deployId);
  tokenUrl.searchParams.set("site_id", siteId);
  const accessResponse = await requestJson("access_control_token", tokenUrl.href, NETLIFY_ACCESS_ORIGIN, token);
  const accessToken = accessResponse.accessControlToken;
  if (typeof accessToken !== "string" || accessToken.length < 8) {
    throw new Error("Netlify access-control token response did not contain a usable accessControlToken");
  }
  secretValues.add(accessToken);
  appendRecord({ event: "log_transport", endpoint: LOG_SOCKET_URL, access_control_token: "obtained; value withheld" });
  const capture = await websocketCapture(deployId, siteId, accessToken);
  appendRecord({ event: "diagnostic_complete", source: "deploy_logs_websocket", ...capture, deploy_state: deploy.state, build_state: build?.state ?? "unavailable" });
}

function selfTestRedactor() {
  const fake = "NetlifySecretValue_0123456789";
  const genericBearer = "UnlistedBearerCredential_0123456789";
  const signedAccess = "SignedAccessCredential_0123456789";
  const source = `Bearer ${fake} Bearer ${genericBearer} https://example.test/build?access_token=${signedAccess}&keep=1 NETLIFY_AUTH_TOKEN=${fake} GITHUB_TOKEN=PatternCredential_0123456789\nAuthorization: Basic BasicCredential_0123456789\nclient_secret=OAuthSecret_0123456789 Cookie: session=CookieCredential_0123456789\nhttps://user:password@example.test/ stage publish failed: missing asset`;
  const result = redactText(source, [fake]);
  for (const secret of [fake, genericBearer, signedAccess, "PatternCredential_0123456789", "BasicCredential_0123456789", "OAuthSecret_0123456789", "CookieCredential_0123456789", "user:password"]) {
    assert.equal(result.includes(secret), false);
  }
  assert.match(result, /Bearer \[REDACTED\]/);
  assert.match(result, /access_token=\[REDACTED\]/);
  assert.match(result, /GITHUB_TOKEN=\[REDACTED\]/);
  assert.match(result, /stage publish failed: missing asset/);
  process.stdout.write("redactor edge checks passed\n");
}

if (process.argv[2] === "--self-test-redactor") {
  selfTestRedactor();
} else {
  main().catch((error) => {
    try {
      appendRecord({ event: "diagnostic_error", message: error?.message ?? "unknown error" });
    } catch {
      process.stderr.write("diagnostic failed; safe artifact could not be written\n");
    }
    process.exitCode = 1;
  });
}

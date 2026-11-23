package poolconfig

import "time"

type rejection struct {
	name   string
	mutate func(p *Pool)
	want   string
}

const (
	nameRule  = "must be 1 to 32"
	poolOwned = "set by the pool"
)

// rejections starts from testdata/pool.yaml, breaks one rule each and expects
// the error to mention want.
const wantDevcontainer = "devcontainerPath"

var rejections = []rejection{
	{"name uppercase", func(p *Pool) { p.Metadata.Name = "Demo" }, nameRule},
	{
		"name too long",
		func(p *Pool) { p.Metadata.Name = "a23456789012345678901234567890123" },
		nameRule,
	},
	{
		"absolute devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = "/etc/dc.json" },
		wantDevcontainer,
	},
	{
		"escaping devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = "../x.json" },
		wantDevcontainer,
	},
	{
		"rooted windows devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = `\etc\dc.json` },
		wantDevcontainer,
	},
	{
		"drive devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = "C:dc.json" },
		wantDevcontainer,
	},
	{
		"escaping windows devcontainer",
		func(p *Pool) { p.Spec.Source.DevcontainerPath = `..\x.json` },
		wantDevcontainer,
	},
	{"no concurrency", func(p *Pool) { p.Spec.Capacity.MaxConcurrent = -1 }, "maxConcurrent"},
	{"negative queue", func(p *Pool) { p.Spec.Capacity.MaxQueued = -1 }, "maxQueued"},
	{"too many warm", func(p *Pool) { p.Spec.Capacity.Warm = 5 }, "capacity.warm"},
	{
		"idle over ttl",
		func(p *Pool) { p.Spec.Lifecycle.IdleTimeout = dur(9 * time.Hour) },
		"idleTimeout",
	},
	{
		"negative job timeout",
		func(p *Pool) { p.Spec.Lifecycle.JobTimeout = dur(-time.Minute) },
		"jobTimeout",
	},
	{"negative ttl", func(p *Pool) { p.Spec.Lifecycle.TTL = dur(-time.Minute) }, "lifecycle.ttl"},
	{"no agents", func(p *Pool) { p.Spec.Agents = nil }, "at least one agent"},
	{
		"bad agent name",
		func(p *Pool) { p.Spec.Agents["Bad"] = Agent{Command: []string{"x"}} },
		"spec.agents.<key>: must be 1 to 63",
	},
	{"empty command", func(p *Pool) { setAgent(p, func(a *Agent) { a.Command = nil }) }, "command"},
	{
		"bad env name",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["1X"] = "v" }) },
		"spec.agents.claude.env: names must start with a letter",
	},
	{
		"proxy env",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["https_proxy"] = "x" }) },
		poolOwned,
	},
	{
		"reserved env mixed case",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["GH_token"] = "x" }) },
		poolOwned,
	},
	{
		"enterprise token env",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["GITHUB_ENTERPRISE_TOKEN"] = "x" }) },
		poolOwned,
	},
	{
		"git config env",
		func(p *Pool) { setAgent(p, func(a *Agent) { a.Env["GIT_CONFIG_COUNT"] = "1" }) },
		poolOwned,
	},
	{"zero retention", func(p *Pool) { p.Spec.Audit.RetainDays = -1 }, "retainDays"},
	{"relative audit dir", func(p *Pool) { p.Spec.Audit.Dir = "audit" }, "absolute"},
}

func dur(d time.Duration) Duration {
	return Duration{Duration: d}
}

func setAgent(p *Pool, mutate func(a *Agent)) {
	agent := p.Spec.Agents["claude"]
	mutate(&agent)
	p.Spec.Agents["claude"] = agent
}

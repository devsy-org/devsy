'use client';

import { useEffect, useState } from 'react';
import { useTheme } from 'next-themes';
import './home.css';

export default function HomePage() {
  const [navOpen, setNavOpen] = useState(false);
  const [mounted, setMounted] = useState(false);
  const { resolvedTheme, setTheme } = useTheme();

  useEffect(() => {
    setMounted(true);
  }, []);

  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 992px)');
    const onChange = (e: MediaQueryListEvent) => {
      if (e.matches) setNavOpen(false);
    };
    desktop.addEventListener('change', onChange);
    return () => desktop.removeEventListener('change', onChange);
  }, []);

  const closeMenu = () => setNavOpen(false);
  const isDark = mounted ? resolvedTheme === 'dark' : false;
  const themeLabel = isDark ? 'Switch to light theme' : 'Switch to dark theme';

  return (
    <div className="home-page">
      <header>
        <div className="wrap nav">
          <a className="brand" href="/" aria-label="Devsy home">
            <svg width="30" height="30" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
              <rect x="0" y="0" width="1024" height="1024" rx="200" ry="200" fill="#7C5CFF" />
              <rect x="778" y="144" width="56" height="736" rx="12" fill="#fff" />
              <rect x="778" y="496" width="24" height="192" fill="#fff" />
              <circle cx="506" cy="592" r="288" fill="none" stroke="#fff" strokeWidth="56" />
            </svg>
            devsy
          </a>
          <button
            className="nav-toggle"
            id="navToggle"
            type="button"
            aria-expanded={navOpen}
            aria-controls="navMenu"
            aria-label={navOpen ? 'Close menu' : 'Open menu'}
            onClick={() => setNavOpen((open) => !open)}
          >
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
              <path d="M3 6h18M3 12h18M3 18h18" />
            </svg>
          </button>
          <div className={`nav-menu${navOpen ? ' open' : ''}`} id="navMenu">
            <nav className="nav-links">
              <a href="#features" onClick={closeMenu}>
                Features
              </a>
              <a href="#agents" onClick={closeMenu}>
                Agents
              </a>
              <a href="#anywhere" onClick={closeMenu}>
                Deploy anywhere
              </a>
              <a href="#how" onClick={closeMenu}>
                How it works
              </a>
              <a href="#services" onClick={closeMenu}>
                Services
              </a>
              <a href="/docs/what-is-devsy" onClick={closeMenu}>
                Docs
              </a>
            </nav>
            <div className="nav-cta">
              <span className="nav-divider" aria-hidden="true" />
              <button
                className="theme-toggle"
                id="themeToggle"
                type="button"
                aria-label={themeLabel}
                title={themeLabel}
                onClick={() => setTheme(isDark ? 'light' : 'dark')}
              >
                {isDark ? (
                  <svg
                    width="18"
                    height="18"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <circle cx="12" cy="12" r="4" />
                    <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
                  </svg>
                ) : (
                  <svg
                    width="18"
                    height="18"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" />
                  </svg>
                )}
              </button>
              <a className="btn btn-primary" href="/docs/getting-started/install" onClick={closeMenu}>
                Get Started
              </a>
            </div>
          </div>
        </div>
      </header>

      <div className="home-content">
        {/* HERO */}
        <section className="hero">
          <div className="wrap">
            <h1>
              Ship from <span className="grad">day zero</span>.
            </h1>
            <p className="hero-sub">Standardized workspaces, engineering at scale.</p>
            <p className="lede">
              Devsy gives every engineer the same environment. Cut hardware cost, shorten onboarding, and raise
              developer productivity. Deploy across Docker, Kubernetes, cloud providers, and SSH hosts.
            </p>
            <div className="hero-cta">
              <a className="btn btn-primary" href="/docs/getting-started/install">
                Get Started
              </a>
              <a className="btn btn-ghost" href="/docs/what-is-devsy">
                Read the docs
              </a>
            </div>
            <div className="hero-media">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src="/docs/media/devsy-flow.gif" alt="Devsy launching and connecting to a workspace" loading="lazy" />
            </div>
          </div>
        </section>

        {/* FEATURES */}
        <section id="features">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">Why Devsy</div>
              <h2>One environment definition. Every backend.</h2>
              <p>
                Define a workspace once in <code>devcontainer.json</code>. Give your whole team the same toolchain,
                with no lock-in and no drift.
              </p>
            </div>
            <div className="grid">
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M4 7V5a2 2 0 0 1 2-2h2M4 17v2a2 2 0 0 0 2 2h2M20 7V5a2 2 0 0 0-2-2h-2M20 17v2a2 2 0 0 1-2 2h-2" />
                    <rect x="8" y="8" width="8" height="8" rx="1" />
                  </svg>
                </div>
                <h3>Standardized environments</h3>
                <p>
                  Every engineer gets the same dependencies, config, and Git and Docker credentials. New hires ship
                  from day zero instead of losing days to setup.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="4" y="4" width="16" height="16" rx="2" />
                    <rect x="9" y="9" width="6" height="6" />
                    <path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3" />
                  </svg>
                </div>
                <h3>Hardware on demand</h3>
                <p>
                  Provision cloud machines with the CPU, memory, and GPU your workload needs, up to the largest
                  instances your cloud offers. Move a workspace from a laptop to a multi-GPU box without changing
                  your config.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6" />
                  </svg>
                </div>
                <h3>Cost efficiency</h3>
                <p>
                  Run on infrastructure you own. Prebuilds and inactivity-based auto-shutdown mean you pay for
                  capacity in use.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M18 6 6 18M8 6h10v10" />
                  </svg>
                </div>
                <h3>No vendor lock-in</h3>
                <p>
                  Local, cloud, Kubernetes, or remote SSH. Switch a workspace&apos;s provider with a single command.
                  Your definition stays the same.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="2" y="3" width="20" height="14" rx="2" />
                    <path d="M8 21h8M12 17v4" />
                  </svg>
                </div>
                <h3>IDE support</h3>
                <p>Use the IDE you know: VS Code, Cursor, Zed, the full JetBrains suite, and more.</p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 2v4M12 18v4M4.9 4.9l2.8 2.8M16.3 16.3l2.8 2.8M2 12h4M18 12h4M4.9 19.1l2.8-2.8M16.3 7.7l2.8-2.8" />
                  </svg>
                </div>
                <h3>Extensible</h3>
                <p>If a provider doesn&apos;t exist for your platform, build your own with a small provider spec.</p>
              </div>
            </div>
          </div>
        </section>

        {/* AGENTS */}
        <section id="agents">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">For AI agents</div>
              <h2>Secure, disposable environments for every agent you run</h2>
              <p>
                AI coding agents execute real commands with real access. Devsy gives each one its own isolated,
                reproducible container, so agents run without putting your host, secrets, or other projects at risk.
              </p>
            </div>
            <div className="grid">
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <rect x="5" y="11" width="14" height="10" rx="2" />
                    <path d="M8 11V7a4 4 0 0 1 8 0v4" />
                  </svg>
                </div>
                <h3>Sandboxed by default</h3>
                <p>
                  Each agent gets its own container with filesystem, network, and process isolation. A bad command
                  or a prompt-injected instruction can&apos;t reach your host, your other projects, or your secrets.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <circle cx="8" cy="15" r="4" />
                    <path d="M10.5 12.5 19 4M19 4h-4M19 4v4" />
                  </svg>
                </div>
                <h3>Scoped credentials</h3>
                <p>
                  Git and Docker credentials sync into the workspace, not the agent&apos;s host. Revoke or rotate a
                  workspace&apos;s access without touching your machine.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M3 12a9 9 0 1 0 3-6.7M3 4v5h5" />
                  </svg>
                </div>
                <h3>Destroy and rebuild in seconds</h3>
                <p>
                  Something looks off? Tear the container down and rebuild clean from <code>devcontainer.json</code>.
                  No manual cleanup, no lingering state.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 3 2 8l10 5 10-5-10-5ZM2 13l10 5 10-5" />
                  </svg>
                </div>
                <h3>One environment, many agents</h3>
                <p>
                  Spin up as many isolated agent workspaces as you need from the same <code>devcontainer.json</code>{' '}
                  your team uses for development.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" />
                  </svg>
                </div>
                <h3>Run where it&apos;s cheapest</h3>
                <p>
                  Local Docker for quick tasks, Kubernetes or cloud providers when you need to fan out dozens of
                  agents at once. Auto-shutdown keeps idle agent workspaces from burning budget.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M20 6 9 17l-5-5" />
                  </svg>
                </div>
                <h3>Same setup, every time</h3>
                <p>
                  No environment drift between agent runs. Every workspace starts from the same reproducible
                  definition, so agent output stays comparable.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* DEPLOY ANYWHERE */}
        <section id="anywhere" className="band">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">Deploy anywhere</div>
              <h2>Workspaces run where your work does</h2>
              <p>
                Devsy builds on the open Devcontainer standard, so workspaces stay portable and repeatable across
                every provider backend.
              </p>
            </div>
            <div className="targets">
              <div className="target">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
                    <path d="M13.983 11.078h2.119a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.119a.185.185 0 00-.185.185v1.888c0 .102.083.185.185.185m-2.954-5.43h2.118a.186.186 0 00.186-.186V3.574a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.185m0 2.716h2.118a.187.187 0 00.186-.186V6.29a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.887c0 .102.082.185.185.186m-2.93 0h2.12a.186.186 0 00.184-.186V6.29a.185.185 0 00-.185-.185H8.1a.185.185 0 00-.185.185v1.887c0 .102.083.185.185.186m-2.964 0h2.119a.186.186 0 00.185-.186V6.29a.185.185 0 00-.185-.185H5.136a.186.186 0 00-.186.185v1.887c0 .102.084.185.186.186m5.893 2.715h2.118a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.185m-2.93 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.083.185.185.185m-2.964 0h2.119a.185.185 0 00.185-.185V9.006a.185.185 0 00-.184-.186h-2.12a.186.186 0 00-.186.186v1.887c0 .102.084.185.186.185m-2.92 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.082.185.185.185M23.763 9.89c-.065-.051-.672-.51-1.954-.51-.338.001-.676.03-1.01.087-.248-1.7-1.653-2.53-1.716-2.566l-.344-.199-.226.327c-.284.438-.49.922-.612 1.43-.23.97-.09 1.882.403 2.661-.595.332-1.55.413-1.744.42H.751a.751.751 0 00-.75.748 11.376 11.376 0 00.692 4.062c.545 1.428 1.355 2.48 2.41 3.124 1.18.723 3.1 1.137 5.275 1.137.983.003 1.963-.086 2.93-.266a12.248 12.248 0 003.823-1.389c.98-.567 1.86-1.288 2.61-2.136 1.252-1.418 1.998-2.997 2.553-4.4h.221c1.372 0 2.215-.549 2.68-1.009.309-.293.55-.65.707-1.046l.098-.288Z" />
                  </svg>
                </div>
                <strong>Docker</strong>
                <span>Local containers on any laptop</span>
              </div>
              <div className="target">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor" xmlns="http://www.w3.org/2000/svg">
                    <path d="M10.204 14.35l.007.01-.999 2.413a5.171 5.171 0 0 1-2.075-2.597l2.578-.437.004.005a.44.44 0 0 1 .484.606zm-.833-2.129a.44.44 0 0 0 .173-.756l.002-.011L7.585 9.7a5.143 5.143 0 0 0-.73 3.255l2.514-.725.002-.009zm1.145-1.98a.44.44 0 0 0 .699-.337l.01-.005.15-2.62a5.144 5.144 0 0 0-3.01 1.442l2.147 1.523.004-.002zm.76 2.75l.723.349.722-.347.18-.78-.5-.623h-.804l-.5.623.179.779zm1.5-3.095a.44.44 0 0 0 .7.336l.008.003 2.134-1.513a5.188 5.188 0 0 0-2.992-1.442l.148 2.615.002.001zm10.876 5.97l-5.773 7.181a1.6 1.6 0 0 1-1.248.594l-9.261.003a1.6 1.6 0 0 1-1.247-.596l-5.776-7.18a1.583 1.583 0 0 1-.307-1.34L2.1 5.573c.108-.47.425-.864.863-1.073L11.305.513a1.606 1.606 0 0 1 1.385 0l8.345 3.985c.438.209.755.604.863 1.073l2.062 8.955c.108.47-.005.963-.308 1.34zm-3.289-2.057c-.042-.01-.103-.026-.145-.034-.174-.033-.315-.025-.479-.038-.35-.037-.638-.067-.895-.148-.105-.04-.18-.165-.216-.216l-.201-.059a6.45 6.45 0 0 0-.105-2.332 6.465 6.465 0 0 0-.936-2.163c.052-.047.15-.133.177-.159.008-.09.001-.183.094-.282.197-.185.444-.338.743-.522.142-.084.273-.137.415-.242.032-.024.076-.062.11-.089.24-.191.295-.52.123-.736-.172-.216-.506-.236-.745-.045-.034.027-.08.062-.111.088-.134.116-.217.23-.33.35-.246.25-.45.458-.673.609-.097.056-.239.037-.303.033l-.19.135a6.545 6.545 0 0 0-4.146-2.003l-.012-.223c-.065-.062-.143-.115-.163-.25-.022-.268.015-.557.057-.905.023-.163.061-.298.068-.475.001-.04-.001-.099-.001-.142 0-.306-.224-.555-.5-.555-.275 0-.499.249-.499.555l.001.014c0 .041-.002.092 0 .128.006.177.044.312.067.475.042.348.078.637.056.906a.545.545 0 0 1-.162.258l-.012.211a6.424 6.424 0 0 0-4.166 2.003 8.373 8.373 0 0 1-.18-.128c-.09.012-.18.04-.297-.029-.223-.15-.427-.358-.673-.608-.113-.12-.195-.234-.329-.349-.03-.026-.077-.062-.111-.088a.594.594 0 0 0-.348-.132.481.481 0 0 0-.398.176c-.172.216-.117.546.123.737l.007.005.104.083c.142.105.272.159.414.242.299.185.546.338.743.522.076.082.09.226.1.288l.16.143a6.462 6.462 0 0 0-1.02 4.506l-.208.06c-.055.072-.133.184-.215.217-.257.081-.546.11-.895.147-.164.014-.305.006-.48.039-.037.007-.09.02-.133.03l-.004.002-.007.002c-.295.071-.484.342-.423.608.061.267.349.429.645.365l.007-.001.01-.003.129-.029c.17-.046.294-.113.448-.172.33-.118.604-.217.87-.256.112-.009.23.069.288.101l.217-.037a6.5 6.5 0 0 0 2.88 3.596l-.09.218c.033.084.069.199.044.282-.097.252-.263.517-.452.813-.091.136-.185.242-.268.399-.02.037-.045.095-.064.134-.128.275-.034.591.213.71.248.12.556-.007.69-.282v-.002c.02-.039.046-.09.062-.127.07-.162.094-.301.144-.458.132-.332.205-.68.387-.897.05-.06.13-.082.215-.105l.113-.205a6.453 6.453 0 0 0 4.609.012l.106.192c.086.028.18.042.256.155.136.232.229.507.342.84.05.156.074.295.145.457.016.037.043.09.062.129.133.276.442.402.69.282.247-.118.341-.435.213-.71-.02-.039-.045-.096-.065-.134-.083-.156-.177-.261-.268-.398-.19-.296-.346-.541-.443-.793-.04-.13.007-.21.038-.294-.018-.022-.059-.144-.083-.202a6.499 6.499 0 0 0 2.88-3.622c.064.01.176.03.213.038.075-.05.144-.114.28-.104.266.039.54.138.87.256.154.06.277.128.448.173.036.01.088.019.13.028l.009.003.007.001c.297.064.584-.098.645-.365.06-.266-.128-.537-.423-.608zM16.4 9.701l-1.95 1.746v.005a.44.44 0 0 0 .173.757l.003.01 2.526.728a5.199 5.199 0 0 0-.108-1.674A5.208 5.208 0 0 0 16.4 9.7zm-4.013 5.325a.437.437 0 0 0-.404-.232.44.44 0 0 0-.372.233h-.002l-1.268 2.292a5.164 5.164 0 0 0 3.326.003l-1.27-2.296h-.01zm1.888-1.293a.44.44 0 0 0-.27.036.44.44 0 0 0-.214.572l-.003.004 1.01 2.438a5.15 5.15 0 0 0 2.081-2.615l-2.6-.44-.004.005z" />
                  </svg>
                </div>
                <strong>Kubernetes</strong>
                <span>Scale across your cluster</span>
              </div>
              <div className="target">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" />
                  </svg>
                </div>
                <strong>Cloud providers</strong>
                <span>On-demand machines of any size</span>
              </div>
              <div className="target">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="m7 11 2-2-2-2" />
                    <path d="M11 13h4" />
                    <rect width="18" height="18" x="3" y="3" rx="2" ry="2" />
                  </svg>
                </div>
                <strong>SSH remote hosts</strong>
                <span>Any reachable server</span>
              </div>
            </div>
          </div>
        </section>

        {/* HOW IT WORKS */}
        <section id="how">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">How it works</div>
              <h2>From definition to running workspace</h2>
              <p>Devsy connects each developer&apos;s local IDE to the infrastructure where work runs.</p>
            </div>
            <div className="steps">
              <div className="step">
                <div className="num">1</div>
                <h3>Define once</h3>
                <p>
                  Commit a <code>devcontainer.json</code> alongside your source. It describes the tools and
                  dependencies your whole team shares.
                </p>
              </div>
              <div className="step">
                <div className="num">2</div>
                <h3>Pick a provider</h3>
                <p>Choose Docker, Kubernetes, a cloud machine, or an SSH host. Providers provision the container for you.</p>
              </div>
              <div className="step">
                <div className="num">3</div>
                <h3>Connect and build</h3>
                <p>
                  Your local IDE attaches to the workspace. The experience holds from laptop to cloud, prototype to
                  production.
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* SERVICES */}
        <section id="services">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">Services</div>
              <h2>Work with the team behind Devsy</h2>
              <p>
                Get hands-on help to standardize your development environments, from a focused audit to a full
                rollout and ongoing support.
              </p>
            </div>
            <div className="grid">
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="m21 21-4.3-4.3" />
                    <circle cx="11" cy="11" r="8" />
                  </svg>
                </div>
                <h3>Dev environment audit</h3>
                <p>
                  A focused review of how your team onboards and runs environments. You get a scored assessment, a
                  ranked list of your top bottlenecks, and a working, standardized devcontainer for one repository.
                  Fixed scope, fixed price.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="m8 3 4 8 5-5 5 15H2L8 3z" />
                  </svg>
                </div>
                <h3>Implementation and rollout</h3>
                <p>
                  We set up Devsy and devcontainers across your repositories. You get standardized toolchains,
                  prebuilds, and local-to-CI parity, so every engineer ships from day zero.
                </p>
              </div>
              <div className="card">
                <div className="ico" aria-hidden="true">
                  <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
                  </svg>
                </div>
                <h3>Support and migration</h3>
                <p>
                  Migrating off an abandoned tool, or want a maintained platform you can rely on? Get
                  ongoing support, upgrades, and hands-on migration help.
                </p>
              </div>
            </div>
            <div className="service-cta">
              <a className="btn btn-primary" href="#contact">
                Get in touch
              </a>
            </div>
          </div>
        </section>

        {/* CONTACT */}
        <section id="contact" className="band">
          <div className="wrap">
            <div className="section-head">
              <div className="kicker">Contact</div>
              <h2>Tell us about your team</h2>
              <p>
                Tell us where your dev-environment setup slows your team down, and we&apos;ll reply with how we can
                help.
              </p>
            </div>
            <form name="contact" method="POST" data-netlify="true" netlify-honeypot="bot-field" className="contact-form">
              <input type="hidden" name="form-name" value="contact" />
              <p className="form-hidden">
                <label>
                  Skip this field if you&apos;re human: <input name="bot-field" />
                </label>
              </p>
              <div className="form-row">
                <div className="field">
                  <label htmlFor="name">Name</label>
                  <input id="name" type="text" name="name" autoComplete="name" required />
                </div>
                <div className="field">
                  <label htmlFor="email">Work email</label>
                  <input id="email" type="email" name="email" autoComplete="email" required />
                </div>
              </div>
              <div className="field">
                <label htmlFor="company">
                  Company <span className="optional">(optional)</span>
                </label>
                <input id="company" type="text" name="company" autoComplete="organization" />
              </div>
              <div className="field">
                <label htmlFor="message">How can we help?</label>
                <textarea
                  id="message"
                  name="message"
                  required
                  placeholder="Tell us about your team size, current setup, and what you want to improve."
                />
              </div>
              <div className="form-actions">
                <button type="submit" className="btn btn-primary">
                  Send message
                </button>
              </div>
              <p className="contact-note">We&apos;ll get back to you within a couple of business days.</p>
            </form>
          </div>
        </section>

        {/* CTA */}
        <section>
          <div className="wrap">
            <div className="cta">
              <h2>Scale your engineering, not your setup.</h2>
              <p>Get Devsy as a desktop app or a command-line tool and launch your first standardized workspace.</p>
              <div className="hero-cta">
                <a className="btn btn-primary" href="/docs/getting-started/install">
                  Download and install
                </a>
                <a className="btn btn-ghost" href="https://github.com/devsy-org/devsy">
                  View on GitHub
                </a>
              </div>
            </div>
          </div>
        </section>
      </div>

      <footer>
        <div className="wrap foot">
          <a className="brand" href="/" style={{ fontSize: 18 }} aria-label="Devsy home">
            <svg width="24" height="24" viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
              <rect x="0" y="0" width="1024" height="1024" rx="200" ry="200" fill="#7C5CFF" />
              <rect x="778" y="144" width="56" height="736" rx="12" fill="#fff" />
              <rect x="778" y="496" width="24" height="192" fill="#fff" />
              <circle cx="506" cy="592" r="288" fill="none" stroke="#fff" strokeWidth="56" />
            </svg>
            devsy
          </a>
          <nav className="foot-links">
            <a href="/docs/what-is-devsy">Documentation</a>
            <a href="/docs/getting-started/install">Install</a>
            <a href="#services">Services</a>
            <a href="https://github.com/devsy-org/devsy">GitHub</a>
          </nav>
          <span>&copy; {new Date().getFullYear()} Devsy, Inc.</span>
        </div>
      </footer>
    </div>
  );
}

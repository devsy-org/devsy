import { interestMode } from './interest';
import './pools.css';

const BENEFITS = [
  {
    title: 'The same environment as your team.',
    text: "Each agent session will start from your repository's devcontainer.json and be deleted when it ends.",
  },
  {
    title: 'Network access you control.',
    text: 'Connections will be blocked unless you list them. Each allowed or blocked connection will be logged.',
  },
  {
    title: 'Credentials that expire.',
    text: 'Agents will get short-lived, narrowly scoped access instead of long-lived secrets.',
  },
  {
    title: 'A record security can read.',
    text: 'Every session will show what it reached and what it used, and you will be able to export it.',
  },
];

const STEPS = [
  {
    title: 'Describe the pool.',
    text: 'You will put a pool.yaml in git: the repository, the devcontainer, the allowed hosts, the credentials and the limits.',
  },
  {
    title: 'Run it where you want.',
    text: 'You will start a pool on your own machine or cluster. It will connect out to a control plane and need no inbound port.',
  },
  {
    title: 'Let agents work, then review.',
    text: 'Each session will get its own workspace. Afterwards, you will read the record of what it did.',
  },
];

const ROADMAP = [
  {
    id: 'now',
    title: 'Now',
    items: [
      'Open-source workspaces from devcontainer.json on Docker, Kubernetes, cloud machines and SSH hosts',
      'Pool configuration and validation in the open-source repository',
      'A local audit log file and a command to read it, in the open-source repository',
      'Control plane foundations: organizations, members and API keys, not public yet',
    ],
  },
  {
    id: 'next',
    title: 'Next',
    items: [
      'Run a pool on one Docker host with deny-by-default network access',
      'Short-lived credentials for agents',
      'Run an agent CLI headless inside a pool',
      'A read-only web view of pools and sessions',
    ],
  },
  {
    id: 'later',
    title: 'Later',
    items: [
      'Pools on Kubernetes',
      "Start sessions from coding agent vendors' self-hosted worker paths",
      'A control plane: central policy, fleet view, audit search and export',
      'Company sign-in and roles',
      "The same policy and audit for developers' own workspaces",
    ],
  },
];

function Code({ children }: { children: string }) {
  return <code>{children}</code>;
}

function withCode(text: string) {
  return text.split(/(devcontainer\.json|pool\.yaml)/).map((part, index) =>
    index % 2 === 1 ? <Code key={index}>{part}</Code> : part,
  );
}

export function PoolsSection() {
  return (
    <section id="pools" aria-labelledby="pools-title">
      <div className="wrap">
        <div className="section-head">
          <p className="kicker">Coming next</p>
          <h2 id="pools-title">Devsy Pools</h2>
          <p className="pools-badge">In development. Some parts are in the open-source repository today.</p>
          <p className="pools-sub">A governed place for coding agents to run, on your own machines.</p>
          <p className="pools-lede">
            Devsy Pools will run coding agents in a fresh workspace per session, with limited network access,
            short-lived credentials and a record of what happened.
          </p>
        </div>
        <h3 className="pools-subhead pools-subhead-first">What Devsy Pools is designed to do</h3>
        <ul className="pools-benefits">
          {BENEFITS.map((benefit) => (
            <li key={benefit.title}>
              <h3>{benefit.title}</h3>
              <p>{withCode(benefit.text)}</p>
            </li>
          ))}
        </ul>
        <h3 className="pools-subhead">How it is planned to work</h3>
        <ol className="pools-steps">
          {STEPS.map((step) => (
            <li key={step.title}>
              <h4>{step.title}</h4>
              <p>{withCode(step.text)}</p>
            </li>
          ))}
        </ol>
        <div className="pools-facts">
          <p>
            <strong>Who it is for.</strong> Platform and developer-experience teams that run coding agents on
            their own infrastructure, and the security teams that must sign off on them.
          </p>
          <p>
            <strong>Free or paid.</strong> One host will stay free and open source. A control plane for many hosts
            and teams is planned as a paid product.
          </p>
          <div className="pools-links">
            <a className="text-link" href="/docs/what-is-devsy">
              Read the docs
            </a>
            {interestMode() === 'on' ? (
              <a className="text-link" href="#pools-interest">
                Register interest
              </a>
            ) : null}
          </div>
        </div>
      </div>
    </section>
  );
}

export function Roadmap() {
  return (
    <section id="roadmap" className="pools-roadmap" aria-labelledby="roadmap-title">
      <div className="wrap">
        <div className="section-head">
          <p className="kicker">Roadmap</p>
          <h2 id="roadmap-title">What we are building, in order</h2>
        </div>
        <div className="roadmap-grid">
          {ROADMAP.map((column) => (
            <div key={column.id} className="roadmap-col" data-column={column.id}>
              <h3>{column.title}</h3>
              <ul>
                {column.items.map((item) => (
                  <li key={item}>{withCode(item)}</li>
                ))}
              </ul>
            </div>
          ))}
        </div>
        <p className="roadmap-note">
          Plans change. This is what we intend to build, in this order.
          {interestMode() === 'on' ? (
            <>
              {' '}
              <a className="text-link" href="#pools-interest">
                Register interest
              </a>
            </>
          ) : null}
        </p>
      </div>
    </section>
  );
}

'use client';

import { useEffect, useRef, useState } from 'react';

const scenes = [
  {
    title: 'Choose where it runs',
    description:
      'Local containers, microVMs, your cluster, or a remote machine. Start with a provider that fits your work.',
    image: '/docs/media/home-providers.webp',
    alt: 'Devsy Desktop provider chooser with local and remote options, including Docker, MicroSandbox, SSH, and Kubernetes.',
  },
  {
    title: 'Bring your project',
    description:
      'Use your repository’s devcontainer.json, or start with a template. Review your choices before launching.',
    image: '/docs/media/home-setup.webp',
    alt: 'Devsy workspace creation wizard with the Node.js template selected and its Git repository ready to use.',
  },
  {
    title: 'Open and build',
    description:
      'Your project, editor, and workspace controls together. Open your IDE or connect over SSH.',
    image: '/docs/media/home-workspace.webp',
    alt: 'A sample running node-api workspace in Devsy, with project information, VS Code, and SSH access.',
  },
];

export function ProductWalkthrough() {
  const [scene, setScene] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [inView, setInView] = useState(false);
  const [pageVisible, setPageVisible] = useState(true);
  const container = useRef<HTMLElement>(null);

  useEffect(() => {
    const motion = window.matchMedia('(prefers-reduced-motion: reduce)');
    setPlaying(!motion.matches);
    const onMotionChange = () => {
      if (motion.matches) setPlaying(false);
    };
    const onVisibilityChange = () => setPageVisible(!document.hidden);
    onVisibilityChange();
    motion.addEventListener('change', onMotionChange);
    document.addEventListener('visibilitychange', onVisibilityChange);
    const observer = new IntersectionObserver(
      ([entry]) => setInView(entry.isIntersecting),
      { threshold: 0.15 },
    );
    if (container.current) observer.observe(container.current);
    return () => {
      observer.disconnect();
      motion.removeEventListener('change', onMotionChange);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, []);

  useEffect(() => {
    if (!playing || !inView || !pageVisible) return;
    const timer = window.setTimeout(() => {
      if (scene === scenes.length - 1) setPlaying(false);
      else setScene(scene + 1);
    }, 6500);
    return () => window.clearTimeout(timer);
  }, [scene, playing, inView, pageVisible]);

  function togglePlayback() {
    if (!playing && scene === scenes.length - 1) setScene(0);
    setPlaying(!playing);
  }

  const playbackLabel = playing
    ? 'Pause walkthrough'
    : scene === scenes.length - 1
      ? 'Replay walkthrough'
      : 'Play walkthrough';

  return (
    <section
      ref={container}
      className="product-tour"
      aria-label="Devsy Desktop walkthrough"
    >
      <div className="tour-heading">
        <span>
          <span className="tour-status-dot" aria-hidden="true" />
          Meet your workspace
        </span>
        <button
          className="tour-playback"
          type="button"
          aria-label={playbackLabel}
          onClick={togglePlayback}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="currentColor"
            aria-hidden="true"
          >
            {playing ? (
              <path d="M4 3h3v10H4zm5 0h3v10H9z" />
            ) : (
              <path d="m5 2 9 6-9 6z" />
            )}
          </svg>
          {playing ? 'Pause' : scene === scenes.length - 1 ? 'Replay' : 'Play'}
        </button>
      </div>
      <div id="tour-screen" className="tour-screen">
        {scenes.map((item, index) => (
          /* biome-ignore lint/performance/noImgElement: Local optimized WebP assets; static export has no image optimizer. */
          <img
            key={item.image}
            src={item.image}
            width={1440}
            height={923}
            alt={item.alt}
            aria-hidden={scene !== index}
            className={scene === index ? 'tour-image is-current' : 'tour-image'}
            loading={index === 0 ? 'eager' : 'lazy'}
            fetchPriority={index === 0 ? 'high' : 'auto'}
            decoding="async"
          />
        ))}
      </div>
      <fieldset className="tour-steps" aria-label="Walkthrough steps">
        {scenes.map((item, index) => (
          <button
            key={item.title}
            type="button"
            aria-label={`Step ${index + 1}: ${item.title}`}
            aria-pressed={scene === index}
            aria-controls="tour-screen"
            className={scene === index ? 'tour-step is-current' : 'tour-step'}
            onFocus={() => setPlaying(false)}
            onClick={() => {
              setPlaying(false);
              setScene(index);
            }}
          >
            <span className="tour-step-number" aria-hidden="true">
              0{index + 1}
            </span>
            <span>{item.title}</span>
          </button>
        ))}
      </fieldset>
      <div className="tour-descriptions">
        {scenes.map((item, index) => (
          <p
            key={item.title}
            aria-hidden={scene !== index}
            className={
              scene === index
                ? 'tour-description is-current'
                : 'tour-description'
            }
          >
            {item.description}
          </p>
        ))}
      </div>
      <p className="tour-note">
        Devsy Desktop · Example workspace and sample configuration
      </p>
    </section>
  );
}

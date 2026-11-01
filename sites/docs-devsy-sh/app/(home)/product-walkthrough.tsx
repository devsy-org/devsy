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

const sceneDuration = 6500;

export function ProductWalkthrough() {
  const [scene, setScene] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [inView, setInView] = useState(false);
  const [pageVisible, setPageVisible] = useState(true);
  const container = useRef<HTMLElement>(null);
  const elapsed = useRef(0);

  function resetProgress() {
    elapsed.current = 0;
    container.current?.style.setProperty('--tour-progress', '0');
  }

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
    let previous = performance.now();
    let frame: number;
    const advance = (now: number) => {
      elapsed.current = Math.min(
        sceneDuration,
        elapsed.current + now - previous,
      );
      previous = now;
      container.current?.style.setProperty(
        '--tour-progress',
        String(elapsed.current / sceneDuration),
      );
      if (elapsed.current < sceneDuration) {
        frame = window.requestAnimationFrame(advance);
      } else {
        elapsed.current = 0;
        container.current?.style.setProperty('--tour-progress', '0');
        setScene((scene + 1) % scenes.length);
      }
    };
    frame = window.requestAnimationFrame(advance);
    return () => window.cancelAnimationFrame(frame);
  }, [scene, playing, inView, pageVisible]);

  function togglePlayback() {
    setPlaying(!playing);
  }

  const playbackLabel = playing ? 'Pause walkthrough' : 'Play walkthrough';

  return (
    <section
      ref={container}
      className="product-tour"
      aria-label="Devsy Desktop walkthrough"
    >
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
            className={`tour-step${scene === index ? ' is-current' : ''}${scene > index ? ' is-complete' : ''}`}
            onFocus={() => setPlaying(false)}
            onClick={() => {
              setPlaying(false);
              resetProgress();
              setScene(index);
            }}
          >
            <span className="tour-step-number" aria-hidden="true">
              0{index + 1}
            </span>
            <span>{item.title}</span>
            <span className="tour-progress" aria-hidden="true">
              <span className="tour-progress-fill" />
            </span>
          </button>
        ))}
      </fieldset>
      <div className="tour-footer">
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
          {playing ? 'Pause' : 'Play'}
        </button>
      </div>
    </section>
  );
}

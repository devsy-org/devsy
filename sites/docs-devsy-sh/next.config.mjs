import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  output: 'export',
  reactStrictMode: true,
  images: {
    unoptimized: true, // required for static export
  },
  // Keep the legacy Docusaurus `pages/` directory (still present until Task 3
  // migrates its content into `content/docs`) from being picked up by the
  // Next.js Pages Router, which would otherwise try to compile its
  // Docusaurus-only MDX (e.g. `@theme/Tabs`) as app pages.
  pageExtensions: ['tsx', 'ts', 'jsx', 'js'],
};

export default withMDX(config);

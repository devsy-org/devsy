import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  output: 'export',
  reactStrictMode: true,
  images: {
    unoptimized: true, // required for static export
  },
  // Restrict page extensions to app-router file types only, so the Next.js
  // Pages Router never picks up stray `.md`/`.mdx` files (e.g. content under
  // `content/docs`) as pages.
  pageExtensions: ['tsx', 'ts', 'jsx', 'js'],
};

export default withMDX(config);

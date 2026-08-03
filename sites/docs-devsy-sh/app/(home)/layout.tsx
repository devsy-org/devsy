import type { Metadata } from 'next';
import { HomeLayout } from 'fumadocs-ui/layouts/home';
import { baseOptions } from '@/lib/layout.shared';

export const metadata: Metadata = {
  title: 'Devsy: Standardized development workspaces, engineering at scale',
  description:
    'Devsy gives teams and AI coding agents standardized, isolated workspaces that cut hardware cost, shorten onboarding, and contain agent risk. Deploy across Docker, Kubernetes, cloud providers, and SSH hosts.',
  openGraph: {
    type: 'website',
    siteName: 'Devsy',
    title: 'Devsy: Engineering at scale',
    description:
      'Standardized, reproducible development workspaces that run anywhere: local, cloud, Kubernetes, or remote SSH.',
    url: 'https://www.devsy.sh/',
    images: 'https://www.devsy.sh/docs/media/devsy.png',
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Devsy: Engineering at scale',
    description:
      'Standardized, reproducible development workspaces that run anywhere: local, cloud, Kubernetes, or remote SSH.',
    images: 'https://www.devsy.sh/docs/media/devsy.png',
  },
};

export default function Layout({ children }: LayoutProps<'/'>) {
  const options = baseOptions();

  return (
    <HomeLayout {...options} nav={{ ...options.nav, enabled: false }}>
      {children}
    </HomeLayout>
  );
}

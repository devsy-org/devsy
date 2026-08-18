import type { Metadata } from 'next';
import { HomeLayout } from 'fumadocs-ui/layouts/home';
import { baseOptions } from '@/lib/layout.shared';

export const metadata: Metadata = {
  title: 'Devsy: Devcontainer Workspaces, Any Backend, No Lock-In',
  description:
    'Devsy runs devcontainer-based developer workspaces as Docker containers, Kubernetes pods, or microVMs, for engineers and their AI agents.',
  openGraph: {
    type: 'website',
    siteName: 'Devsy',
    title: 'Devsy: Devcontainer Workspaces, Any Backend',
    description:
      'Developer workspaces built from your devcontainer.json, portable across Docker, Kubernetes, microVMs, cloud, and SSH.',
    url: 'https://www.devsy.sh/',
    images: 'https://www.devsy.sh/docs/media/devsy.png',
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Devsy: Devcontainer Workspaces, Any Backend',
    description:
      'Developer workspaces built from your devcontainer.json, portable across Docker, Kubernetes, microVMs, cloud, and SSH.',
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

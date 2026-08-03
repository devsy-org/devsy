import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import Image from 'next/image';
import { gitConfig } from './shared';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <Image
            src="/docs/media/devsy-logo-horizontal.svg"
            alt="Devsy"
            width={120}
            height={24}
            className="dark:hidden"
          />
          <Image
            src="/docs/media/devsy-logo-horizontal-dark.svg"
            alt="Devsy"
            width={120}
            height={24}
            className="hidden dark:block"
          />
        </>
      ),
      url: 'https://devsy.sh/',
    },
    githubUrl: `https://github.com/${gitConfig.user}/${gitConfig.repo}`,
  };
}

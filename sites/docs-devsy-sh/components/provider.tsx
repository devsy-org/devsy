'use client';
import SearchDialog from '@/components/search';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { type ReactNode } from 'react';

export function Provider({ children }: { children: ReactNode }) {
  return (
    <RootProvider theme={{ defaultTheme: 'light', enableSystem: true }} search={{ SearchDialog }}>
      {children}
    </RootProvider>
  );
}

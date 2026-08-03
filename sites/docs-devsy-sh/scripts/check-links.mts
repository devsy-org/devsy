import { register } from 'fumadocs-mdx/node';

register();

import { type FileObject, printErrors, scanURLs, validateFiles } from 'next-validate-link';

async function checkLinks() {
  const { source } = await import('@/lib/source');

  const scanned = await scanURLs({
    preset: 'next',
    populate: {
      'docs/[[...slug]]': source.getPages().map((page) => ({
        value: { slug: page.slugs },
        hashes: page.data.toc.map((item) => item.url.slice(1)),
      })),
    },
  });

  const files = await Promise.all(
    source.getPages().map(async (page): Promise<FileObject> => ({
      path: page.absolutePath,
      content: await page.data.getText('raw'),
      url: page.url,
      data: page.data,
    })),
  );

  const urlByPath = new Map(files.map((file) => [file.path, file.url]));

  const errors = await validateFiles(files, {
    scanned,
    checkRelativePaths: 'as-url',
    pathToUrl: (filePath) => urlByPath.get(filePath) ?? `/__unresolved__/${filePath}`,
  });
  printErrors(errors, true);
}

void checkLinks();

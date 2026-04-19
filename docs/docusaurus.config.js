__webpack_public_path__ = "/docs/";

module.exports = {
  title: "Devsy docs | DevContainers everywhere",
  tagline: "DevContainers everywhere",
  url: "https://devsy.sh",
  baseUrl: __webpack_public_path__,
  favicon: "/media/devsy-favicon.svg",
  organizationName: "devsy-org", // Usually your GitHub org/user name.
  projectName: "devsy", // Usually your repo name.
  themeConfig: {
    colorMode: {
      defaultMode: "light",
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
    navbar: {
      logo: {
        alt: "devsy",
        src: "/media/devsy-logo-horizontal.svg",
        srcDark: "/media/devsy-logo-horizontal-dark.svg",
        href: "https://devsy.sh/",
        target: "_self",
      },
      items: [
        {
          href: "https://devsy.sh/",
          label: "Website",
          position: "left",
          target: "_self",
        },
        {
          to: "/docs/what-is-devsy",
          label: "Docs",
          position: "left",
        },
        {
          href: "https://devsy.sh/blog",
          label: "Blog",
          position: "left",
          target: "_self",
        },
        {
          href: "https://slack.devsy.sh/",
          className: "slack-link",
          "aria-label": "Slack",
          position: "right",
        },
        {
          href: "https://github.com/devsy-org/devsy",
          className: "github-link",
          "aria-label": "GitHub",
          position: "right",
        },
      ],
    },
    algolia: {
      appId: "Y3XX0IC1ZW",
      apiKey: "cfc452201042c6b5483694d4d0492aa8",
      indexName: "devsy",
      algoliaOptions: {},
      placeholder: "Search...",
      contextualSearch: false,
    },
    footer: {
      style: "light",
      links: [],
      copyright: `Copyright © ${new Date().getFullYear()} <a href="https://devsy.sh/">Devsy, Inc.</a>`,
    },
  },
  presets: [
    [
      "@docusaurus/preset-classic",
      {
        docs: {
          path: "pages",
          routeBasePath: "/",
          sidebarPath: require.resolve("./sidebars.js"),
          editUrl: "https://github.com/devsy-org/devsy/edit/main/docs/",
        },
        theme: {
          customCss: require.resolve("./src/css/custom.css"),
        },
      },
    ],
  ],
  plugins: [],
  scripts: [
    {
      src: "https://cdnjs.cloudflare.com/ajax/libs/clipboard.js/2.0.0/clipboard.min.js",
      async: true,
    },
    {
      src: "/docs/js/custom.js",
      async: true,
    },
  ],
};

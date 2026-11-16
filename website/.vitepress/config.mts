import { defineConfig } from 'vitepress'

const MOTIONCORE = 'https://motioncore.bzy-logics.com'
const SINDRAVA = 'https://sindrava.bzy-logics.com'

export default defineConfig({
  title: 'ratavyr',
  description: 'REST/WebSocket gateway for the MotionCore engine: auth, roles, audit, firmware jobs',
  cleanUrls: true,
  head: [['link', { rel: 'icon', href: '/logo.svg', type: 'image/svg+xml' }]],
  srcExclude: ['README.md'],
  sitemap: { hostname: 'https://ratavyr.bzy-logics.com' },

  themeConfig: {
    logo: '/logo.svg',

    nav: [
      { text: 'Guide', link: '/guide/introduction', activeMatch: '/guide/' },
      { text: 'API', link: '/reference/rest-api', activeMatch: '/reference/' },
      { text: 'Development', link: '/development/engine-contract', activeMatch: '/development/' },
      {
        text: 'Docs',
        items: [
          { text: 'motioncore — engine & firmware', link: MOTIONCORE },
          { text: 'ratavyr — REST/WebSocket gateway', link: '/' },
          { text: 'sindrava — operator UI', link: SINDRAVA },
        ],
      },
    ],

    socialLinks: [{ icon: 'github', link: 'https://github.com/bzy-logics/ratavyr' }],

    editLink: {
      pattern: 'https://github.com/bzy-logics/ratavyr/edit/main/website/:path',
      text: 'Edit this page on GitHub',
    },

    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            { text: 'Introduction', link: '/guide/introduction' },
            { text: 'Getting started', link: '/guide/getting-started' },
            { text: 'Authentication & roles', link: '/guide/authentication' },
            { text: 'Deployment & security', link: '/guide/deployment' },
          ],
        },
        {
          text: 'Elsewhere',
          items: [
            { text: 'MotionCore concepts ↗', link: `${MOTIONCORE}/guide/concepts` },
            { text: 'Robot states ↗', link: `${MOTIONCORE}/guide/robot-states` },
            { text: 'Operator UI ↗', link: SINDRAVA },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'API reference (v1)',
          items: [
            { text: 'REST API', link: '/reference/rest-api' },
            { text: 'WebSocket', link: '/reference/websocket' },
            { text: 'Error codes', link: '/reference/errors' },
            { text: 'Environment variables', link: '/reference/environment' },
          ],
        },
        {
          text: 'Elsewhere',
          items: [{ text: 'Engine gRPC API ↗', link: `${MOTIONCORE}/reference/engine-api` }],
        },
      ],
      '/development/': [
        {
          text: 'Development',
          items: [
            { text: 'Engine contract & codegen', link: '/development/engine-contract' },
            { text: 'Testing', link: '/development/testing' },
            { text: 'Code layout & limitations', link: '/development/layout' },
          ],
        },
      ],
    },

    search: { provider: 'local' },
    outline: { level: [2, 3] },

    footer: {
      message: 'Part of MotionCore. Licensed under GPL-3.0. A software stop never replaces a hardware E-stop circuit.',
    },
  },
})

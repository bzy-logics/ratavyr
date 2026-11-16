---
layout: home

hero:
  name: ratavyr
  text: The HTTP front door to MotionCore
  tagline: A Go gateway that puts a REST and WebSocket API, role-based access, rate limits and an audit log in front of the MotionCore engine — without ever becoming a second source of robot state.
  image:
    src: /logo.svg
    alt: ratavyr
  actions:
    - theme: brand
      text: Getting started
      link: /guide/getting-started
    - theme: alt
      text: REST API
      link: /reference/rest-api
    - theme: alt
      text: Deployment
      link: /guide/deployment

features:
  - title: A gateway, not a brain
    details: The engine is the single authority for robot state, planning and safety. ratavyr authenticates, relays and audits. It caches only the latest telemetry frame.
    link: /guide/introduction
  - title: Three roles
    details: Viewers watch and stop, operators move and build firmware, admins flash. Enforced on every route; stop and E-stop are open to everyone and never rate-limited.
    link: /guide/authentication
  - title: Browser-safe by default
    details: Single-use WebSocket tickets, Origin and Content-Type checks, DNS-rebinding protection, and a refusal to listen beyond loopback without tokens.
    link: /guide/deployment
  - title: Pinned engine contract
    details: Go bindings are generated from a tagged, hash-verified engine-api release. No copy of the proto lives here; CI checks the generated code matches.
    link: /development/engine-contract
---

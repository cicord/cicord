# Security policy

## Supported versions

Cicord is pre-release (`v1alpha1`). Security fixes go to the `main` branch and to the latest
release.

## Reporting a vulnerability

Please **do not open a public issue**. Report it privately through GitHub:
[**Report a vulnerability**](https://github.com/cicord/cicord/security/advisories/new)
(Security tab → Advisories → Report a vulnerability).

Include what you can: the affected component (runtime, plugin host, API, admin console,
portal SDK, CLI), the version or commit, steps to reproduce or a proof of concept, and the
impact. You will get an acknowledgement within a few days; we will keep you informed while a
fix is prepared and credit you in the advisory unless you prefer otherwise.

In scope, among others: escaping the plugin sandbox or its capabilities, bypassing
authentication or roles in the HTTP API, CSRF, server-side request forgery through
`http.fetch` or registries, leaking secrets or tokens, and accepting an unsigned or rolled-back
official registry index. The security model is described in
[Security](https://cicord.dev/docs/self-hosting/security/).

# CODI — CI/CD Oriented Discord Integration

**GitOps for Discord.** Declare your Discord servers and bots in a git repository; the CODI
runtime reconciles them continuously, runs your bots on a shared engine, and extends them
with sandboxed TypeScript plugins from a community registry.

```
git push ──▶ codi serve ──▶ Discord
             ├─ reconciles servers (roles, channels, permissions)
             ├─ runs N bots × M servers with TypeScript plugins
             ├─ admin UI (every change is a git commit)
             └─ API for your community portal (React SDK)
```

> Status: **specification phase** (v1alpha1). No code yet.

## Read this first

| Document | Content |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Components, sync lifecycle, CI/CD flow, UIs, security, roadmap |
| [docs/adr/](docs/adr/README.md) | Architecture decisions (accepted and proposed) |
| [spec/](spec/README.md) | Config format semantics, plugin capabilities, SDK semantics |
| [spec/schemas/](spec/schemas/) | JSON Schemas: `Project`, `Server`, `Bot`, `Plugin` |
| [spec/sdk/codi-sdk.d.ts](spec/sdk/codi-sdk.d.ts) | Plugin SDK contract (TypeScript) |
| [examples/basic/](examples/basic/) | A complete example repository with a `welcome` plugin |

## A taste

```yaml
# servers/ma-commu.yaml
apiVersion: codi.dev/v1alpha1
kind: Server
metadata:
  name: ma-commu
spec:
  discordId: "112233445566778899"
  roles:
    modo: { name: Modo, color: "#e67e22", permissions: [KickMembers, ManageMessages] }
  channels:
    general: { name: général, type: text }
```

```ts
// plugins/welcome/src/index.ts
import { definePlugin } from "@codi/sdk";

export default definePlugin({
  events: {
    async memberJoin(ctx, member) {
      await ctx.server.channel(ctx.config.channel).send(`Bienvenue ${member.mention} !`);
    },
  },
});
```

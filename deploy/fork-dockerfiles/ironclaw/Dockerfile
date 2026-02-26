# IronClaw (tinyland-inc/ironclaw) — standalone Dockerfile
#
# Builds the OpenClaw-based agent with RemoteJuggler config:
# - Enables /v1/responses and /v1/chat/completions HTTP endpoints
# - Sets gateway to bind on LAN (0.0.0.0) for container networking
# - Bakes in openclaw.json config for Aperture API routing
#
# Build context: repo root
# GHCR workflow builds from main branch pushes.

FROM node:22-bookworm@sha256:cd7bcd2e7a1e6f72052feb023c7f6b722205d3fcab7bbcbd2d1bfdab10b1e935

# Install Bun (required for build scripts)
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:${PATH}"

RUN corepack enable

WORKDIR /app
RUN chown node:node /app

COPY --chown=node:node package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc ./
COPY --chown=node:node ui/package.json ./ui/package.json
COPY --chown=node:node patches ./patches
COPY --chown=node:node scripts ./scripts

USER node
RUN pnpm install --frozen-lockfile

USER node
COPY --chown=node:node . .
RUN pnpm build
ENV OPENCLAW_PREFER_PNPM=1
RUN pnpm ui:build

ENV NODE_ENV=production

# --- tinyland customizations ---

# Bake in RemoteJuggler config that enables HTTP endpoints + memory + heartbeat + cron.
# Runtime env vars (ANTHROPIC_API_KEY, OPENCLAW_GATEWAY_TOKEN) are set
# by the K8s deployment, not baked into the image.
COPY --chown=node:node tinyland/openclaw.json /home/node/.openclaw/openclaw.json

# Workspace bootstrap files — copied to /workspace-defaults/ so the K8s init
# container can seed the PVC on first boot without overwriting evolved state.
COPY --chown=node:node tinyland/workspace/ /workspace-defaults/
RUN chmod +x /workspace-defaults/bin/rj-tool

USER node

# Bind to LAN so the adapter sidecar can reach the gateway.
# Auth is required (OPENCLAW_GATEWAY_TOKEN env var at runtime).
CMD ["node", "openclaw.mjs", "gateway", "--bind", "lan", "--port", "18789"]

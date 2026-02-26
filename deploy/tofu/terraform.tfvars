# RemoteJuggler OpenTofu Variables
# Secrets are injected via TF_VAR_* env vars by apply.sh

tailscale_tailnet = "taila4c78d.ts.net"

# Setec hostname: Tailscale Operator appended -1 suffix
gateway_setec_url = "https://setec-1.taila4c78d.ts.net"

# Campaign runner enabled (sidecar in ironclaw pod)
campaign_runner_enabled = true

# GitHub App ID for rj-agent-bot (commit attribution via noreply email)
github_app_id = "2945224"

# Aperture S3 export ingestion (Civo Object Store)
aperture_s3_bucket   = "fuzzy-models"
aperture_s3_prefix   = "aperture/exports/"
aperture_s3_endpoint = "objectstore.nyc1.civo.com"
# S3 credentials injected via TF_VAR_aperture_s3_access_key / TF_VAR_aperture_s3_secret_key

# Agent model selection (Aperture must grant access to the model)
# HexStrike uses Opus for deeper security analysis; IronClaw uses Sonnet for efficiency
hexstrike_model = "claude-opus-4-20250514"

# Pin images to specific sha tags from GHCR builds
# Infrastructure images (from remote-juggler monorepo)
# renovate: image
gateway_image         = "ghcr.io/tinyland-inc/remote-juggler/gateway:sha-65a69ef"
# renovate: image
campaign_runner_image = "ghcr.io/tinyland-inc/remote-juggler/campaign-runner:sha-65a69ef"
# renovate: image
setec_image           = "ghcr.io/tinyland-inc/remote-juggler/setec:sha-65a69ef"
# renovate: image
chapel_binary_image   = "ghcr.io/tinyland-inc/remote-juggler:sha-65a69ef"
# renovate: image
adapter_image         = "ghcr.io/tinyland-inc/remote-juggler/adapter:sha-65a69ef"

# Agent images (from individual repos)
# renovate: image
ironclaw_image     = "ghcr.io/tinyland-inc/ironclaw:sha-fd9691b"
# renovate: image
picoclaw_image     = "ghcr.io/tinyland-inc/picoclaw:sha-89742bd"
# renovate: image
hexstrike_ai_image = "ghcr.io/tinyland-inc/hexstrike-ai:sha-26c4941"

# Scale HexStrike from dormant to active
hexstrike_replicas = 1

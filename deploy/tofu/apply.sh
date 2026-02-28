#!/usr/bin/env bash
# =============================================================================
# OpenTofu wrapper that resolves secrets via rj-gateway before running tofu.
#
# Usage:
#   ./apply.sh init
#   ./apply.sh plan
#   ./apply.sh apply
#   ./apply.sh destroy
#   ./apply.sh output
#
# Secrets are resolved from rj-gateway's composite resolver, dogfooding
# the same credential pipeline that the infrastructure manages.
#
# Override the gateway URL:
#   RJ_GATEWAY_URL=https://rj-gateway.example.ts.net ./apply.sh plan
# =============================================================================
set -euo pipefail

GATEWAY="${RJ_GATEWAY_URL:-http://localhost:8443}"

resolve() {
  local result
  result=$(curl -sf --max-time 10 -X POST "$GATEWAY/resolve" \
    -H "Content-Type: application/json" \
    -d "{\"query\": \"$1\"}" 2>/dev/null | jq -r '.value') || {
    echo "ERROR: Failed to resolve '$1' from gateway at $GATEWAY" >&2
    echo "       Is rj-gateway running? Try: just gateway-run" >&2
    exit 1
  }

  if [ -z "$result" ] || [ "$result" = "null" ]; then
    echo "ERROR: Empty result resolving '$1'" >&2
    exit 1
  fi

  echo "$result"
}

# resolve_or_env: use env var if set, otherwise resolve from gateway.
# Usage: resolve_or_env "ENV_VAR_NAME" "secret-name"
# In CI, secrets are injected as env vars directly (avoids gateway/Setec
# dependency). Locally, falls back to gateway composite resolver.
resolve_or_env() {
  local env_val="${!1:-}"
  if [ -n "$env_val" ]; then
    echo "$env_val"
    return
  fi
  resolve "$2"
}

echo "Resolving secrets from rj-gateway ($GATEWAY)..."

# Infrastructure secrets: allow env override for CI (avoids circular dependency
# where tofu needs S3 creds from gateway, but gateway may not have them).
export TF_VAR_tailscale_oauth_client_id=$(resolve_or_env "TF_VAR_tailscale_oauth_client_id" "tailscale-oauth-client-id")
export TF_VAR_tailscale_oauth_client_secret=$(resolve_or_env "TF_VAR_tailscale_oauth_client_secret" "tailscale-oauth-client-secret")
export TF_VAR_tailscale_auth_key=$(resolve_or_env "TF_VAR_tailscale_auth_key" "tailscale-auth-key")
export TF_VAR_civo_token=$(resolve_or_env "TF_VAR_civo_token" "civo-token")
export AWS_ACCESS_KEY_ID=$(resolve_or_env "AWS_ACCESS_KEY_ID" "civo-object-storage-key")
export AWS_SECRET_ACCESS_KEY=$(resolve_or_env "AWS_SECRET_ACCESS_KEY" "civo-object-storage-secret")

# Gateway S3 credentials (for audit + Aperture export to fuzzy-models bucket)
# Note: AWS_ACCESS_KEY_ID is for the tofu state backend (tinyland-bazel-cache).
# The fuzzy-models bucket has separate credentials stored in Setec.
export TF_VAR_aperture_s3_access_key=$(resolve_or_env "TF_VAR_aperture_s3_access_key" "fuzzy-models-s3-key")
export TF_VAR_aperture_s3_secret_key=$(resolve_or_env "TF_VAR_aperture_s3_secret_key" "fuzzy-models-s3-secret")

# Agent credentials: env override for CI, gateway fallback for local.
export TF_VAR_github_token=$(resolve_or_env "TF_VAR_github_token" "github-token" 2>/dev/null || echo "")
export TF_VAR_gitlab_token=$(resolve_or_env "TF_VAR_gitlab_token" "gitlab-token" 2>/dev/null || echo "")
export TF_VAR_anthropic_api_key=$(resolve_or_env "TF_VAR_anthropic_api_key" "anthropic-api-key" 2>/dev/null || echo "")
export TF_VAR_ghcr_token=$(resolve_or_env "TF_VAR_ghcr_token" "ghcr-token" 2>/dev/null || echo "")

# IronClaw (OpenClaw) gateway auth token
export TF_VAR_openclaw_gateway_token=$(resolve "agents/ironclaw/gateway-token" 2>/dev/null || echo "")

# GitHub App private key for bot-attributed Discussions (issue #9)
export TF_VAR_github_app_private_key=$(resolve_or_env "TF_VAR_github_app_private_key" "github-app-private-key" 2>/dev/null || echo "")

# Web search API key for IronClaw (optional — empty string if not provisioned)
export TF_VAR_brave_api_key=$(resolve "brave-api-key" 2>/dev/null || echo "")

# Agent SSH identity keys (optional — empty string if not yet provisioned)
export TF_VAR_ironclaw_ssh_private_key=$(resolve "agents/ironclaw/ssh-private-key" 2>/dev/null || resolve "agents/openclaw/ssh-private-key" 2>/dev/null || echo "")
export TF_VAR_picoclaw_ssh_private_key=$(resolve "agents/picoclaw/ssh-private-key" 2>/dev/null || echo "")
export TF_VAR_hexstrike_ssh_private_key=$(resolve "agents/hexstrike/ssh-private-key" 2>/dev/null || echo "")
echo "Secrets resolved. Running: tofu $*"

cd "$(dirname "$0")"
exec tofu "$@"

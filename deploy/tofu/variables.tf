# =============================================================================
# Provider credentials (injected via TF_VAR_* from apply.sh)
# =============================================================================

variable "civo_token" {
  description = "Civo API token"
  type        = string
  sensitive   = true
}

variable "tailscale_oauth_client_id" {
  description = "Tailscale OAuth client ID for the operator and provider auth"
  type        = string
  sensitive   = true
}

variable "tailscale_oauth_client_secret" {
  description = "Tailscale OAuth client secret for the operator and provider auth"
  type        = string
  sensitive   = true
}

variable "tailscale_auth_key" {
  description = "Tailscale auth key for service nodes"
  type        = string
  sensitive   = true
}

# =============================================================================
# Agent credentials (seeded into Setec for runtime resolution)
# =============================================================================

variable "github_token" {
  description = "GitHub PAT for API access (seeded into Setec as github-token)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "gitlab_token" {
  description = "GitLab PAT for API access (seeded into Setec as gitlab-token)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "anthropic_api_key" {
  description = "Anthropic API key for agent AI backends (seeded into Setec as anthropic-api-key)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "brave_api_key" {
  description = "Brave Search API key for IronClaw web search (seeded into Setec as brave-api-key)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "openclaw_gateway_token" {
  description = "Bearer token for IronClaw (OpenClaw) gateway HTTP endpoints auth"
  type        = string
  sensitive   = true
  default     = ""
}

# =============================================================================
# Agent SSH identity (ed25519 keypairs stored in Setec)
# =============================================================================

variable "ironclaw_ssh_private_key" {
  description = "IronClaw agent SSH private key (ed25519)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "picoclaw_ssh_private_key" {
  description = "PicoClaw agent SSH private key (ed25519)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "hexstrike_ssh_private_key" {
  description = "HexStrike-AI agent SSH private key (ed25519)"
  type        = string
  sensitive   = true
  default     = ""
}

# =============================================================================
# Agent model selection (routed through Aperture)
# =============================================================================

variable "ironclaw_model" {
  description = "Claude model for IronClaw agent campaigns"
  type        = string
  default     = "claude-sonnet-4-20250514"
}

variable "picoclaw_model" {
  description = "Claude model for PicoClaw lightweight campaigns"
  type        = string
  default     = "claude-haiku-4-5-20251001"
}

variable "hexstrike_model" {
  description = "Claude model for HexStrike-AI security campaigns"
  type        = string
  default     = "claude-sonnet-4-20250514"
}

# =============================================================================
# Container registry (GHCR - all images private)
# =============================================================================

variable "ghcr_username" {
  description = "GitHub username for GHCR image pulls"
  type        = string
  default     = "tinyland-inc"
}

variable "ghcr_token" {
  description = "GitHub PAT or GITHUB_TOKEN with read:packages scope for GHCR pulls"
  type        = string
  sensitive   = true
}

# =============================================================================
# Cluster configuration
# =============================================================================

variable "kubeconfig_path" {
  description = "Path to kubeconfig file"
  type        = string
  default     = "~/.kube/config"
}

variable "namespace" {
  description = "Kubernetes namespace for all resources"
  type        = string
  default     = "fuzzy-dev"
}

# =============================================================================
# Container images
# =============================================================================

variable "setec_image" {
  description = "Setec server container image (built from tailscale/setec source)"
  type        = string
  default     = "ghcr.io/tinyland-inc/remote-juggler/setec:latest" # renovate: image
}

variable "gateway_image" {
  description = "rj-gateway container image"
  type        = string
  default     = "ghcr.io/tinyland-inc/remote-juggler/gateway:latest" # renovate: image
}

variable "ironclaw_image" {
  description = "IronClaw agent container image (OpenClaw-based)"
  type        = string
  default     = "ghcr.io/tinyland-inc/ironclaw:latest" # renovate: image
}

variable "picoclaw_image" {
  description = "PicoClaw agent container image"
  type        = string
  default     = "ghcr.io/tinyland-inc/picoclaw:latest" # renovate: image
}

variable "hexstrike_ai_image" {
  description = "HexStrike-AI pentest agent container image"
  type        = string
  default     = "ghcr.io/tinyland-inc/hexstrike-ai:latest" # renovate: image
}

variable "adapter_image" {
  description = "Campaign adapter sidecar image"
  type        = string
  default     = "ghcr.io/tinyland-inc/remote-juggler/adapter:latest" # renovate: image
}

variable "chapel_binary_image" {
  description = "Chapel CLI container image (for gateway sidecar)"
  type        = string
  default     = "ghcr.io/tinyland-inc/remote-juggler:latest"
}

variable "campaign_runner_image" {
  description = "Campaign runner sidecar image (OpenClaw pod)"
  type        = string
  default     = "ghcr.io/tinyland-inc/remote-juggler/campaign-runner:latest" # renovate: image
}

variable "campaign_runner_enabled" {
  description = "Enable campaign runner sidecar on OpenClaw pod (requires image to be built)"
  type        = bool
  default     = true
}

# =============================================================================
# Resource sizing
# =============================================================================

variable "setec_storage_size" {
  description = "PVC size for Setec data directory"
  type        = string
  default     = "1Gi"
}

variable "hexstrike_results_storage_size" {
  description = "PVC size for HexStrike results"
  type        = string
  default     = "5Gi"
}

variable "hexstrike_replicas" {
  description = "Number of HexStrike replicas (0 = dormant, 1 = active engagement)"
  type        = number
  default     = 0
}

# =============================================================================
# Gateway configuration
# =============================================================================

variable "gateway_setec_url" {
  description = "URL for Setec server (tailnet hostname). Override if Tailscale Operator appends a suffix."
  type        = string
  default     = ""
}

variable "gateway_setec_prefix" {
  description = "Key prefix for Setec secrets"
  type        = string
  default     = "remotejuggler/"
}

variable "gateway_precedence" {
  description = "Secret resolution precedence order"
  type        = list(string)
  default     = ["env", "sops", "kdbx", "setec"]
}

# =============================================================================
# Tailnet configuration
# =============================================================================

variable "tailscale_tailnet" {
  description = "Tailscale tailnet name (e.g. 'example.com' or org name)"
  type        = string
}

variable "aperture_hostname" {
  description = "Tailscale MagicDNS hostname for the Aperture AI gateway"
  type        = string
  default     = "ai"
}

variable "aperture_webhook_enabled" {
  description = "Whether the gateway should accept Aperture webhook callbacks"
  type        = bool
  default     = true
}

variable "aperture_s3_bucket" {
  description = "S3 bucket for Aperture usage export ingestion (empty = disabled)"
  type        = string
  default     = ""
}

variable "aperture_s3_region" {
  description = "AWS region for the Aperture S3 export bucket"
  type        = string
  default     = ""
}

variable "aperture_s3_prefix" {
  description = "S3 key prefix for Aperture export files"
  type        = string
  default     = "aperture/exports/"
}

variable "aperture_s3_endpoint" {
  description = "Custom S3 endpoint for non-AWS stores (e.g. objectstore.nyc1.civo.com)"
  type        = string
  default     = ""
}

variable "aperture_s3_access_key" {
  description = "S3 access key for Aperture export bucket"
  type        = string
  default     = ""
  sensitive   = true
}

variable "aperture_s3_secret_key" {
  description = "S3 secret key for Aperture export bucket"
  type        = string
  default     = ""
  sensitive   = true
}

variable "github_app_id" {
  description = "GitHub App ID for rj-agent-bot (used in noreply email for commit attribution)"
  type        = string
  default     = ""
}

variable "github_app_private_key" {
  description = "GitHub App private key (PEM) for rj-agent-bot (JWT auth for installation tokens)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "skip_setec_seed" {
  description = "Skip Setec seed job (secrets already in Setec). Used in CI to avoid timeout."
  type        = bool
  default     = false
}

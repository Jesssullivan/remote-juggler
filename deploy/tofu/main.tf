# =============================================================================
# RemoteJuggler Infrastructure — OpenTofu Root Module
# =============================================================================
#
# Manages the RemoteJuggler deployment stack on Civo Kubernetes:
#   - Tailscale Operator (Helm) for tailnet-only ingress
#   - Setec secret server
#   - rj-gateway MCP proxy
#   - OpenClaw + HexStrike AI agents
#
# Usage:
#   ./apply.sh init
#   ./apply.sh plan
#   ./apply.sh apply
#
# All secrets are resolved via rj-gateway (dogfooding).
# =============================================================================

provider "civo" {
  token = var.civo_token
}

provider "tailscale" {
  oauth_client_id     = var.tailscale_oauth_client_id
  oauth_client_secret = var.tailscale_oauth_client_secret
  tailnet             = var.tailscale_tailnet
}

provider "kubernetes" {
  config_path = var.kubeconfig_path
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig_path
  }
}

# =============================================================================
# Locals
# =============================================================================

locals {
  labels = {
    "app.kubernetes.io/managed-by" = "opentofu"
    "app.kubernetes.io/part-of"    = "remotejuggler"
  }

  tailscale_tags = {
    gateway  = "tag:rj-gateway"
    setec    = "tag:setec"
    ci_agent = "tag:ci-agent"
  }

  # Setec URL: use override if set, otherwise derive from tailnet.
  # Tailscale Operator may append a suffix (e.g. setec -> setec-1).
  effective_setec_url = var.gateway_setec_url != "" ? var.gateway_setec_url : "https://setec.${var.tailscale_tailnet}"

  # Aperture AI gateway URL -- agents use in-cluster egress service,
  # gateway (on tailnet) uses MagicDNS hostname directly.
  aperture_url         = "http://${var.aperture_hostname}"
  aperture_cluster_url = "http://aperture.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local"

  # Direct Anthropic API -- bypass Aperture until roles are granted via http://ai/ui.
  # Once Aperture roles are configured for the K8s egress proxy identity, change to:
  #   anthropic_direct_url = local.aperture_cluster_url
  anthropic_direct_url = "https://api.anthropic.com"

  # Gateway config JSON (mounted as secret)
  gateway_config = jsonencode({
    listen            = ":443"
    in_cluster_listen = ":8080"
    chapel_binary     = "remote-juggler"
    setec_url         = local.effective_setec_url
    setec_prefix      = var.gateway_setec_prefix
    setec_secrets     = ["neon-database-url", "github-token", "gitlab-token", "attic-token", "anthropic-api-key"]
    precedence        = var.gateway_precedence
    aperture_url      = local.aperture_url
    aperture_webhook  = var.aperture_webhook_enabled
    aperture_s3 = {
      aperture_s3_bucket     = var.aperture_s3_bucket
      aperture_s3_region     = var.aperture_s3_region
      aperture_s3_prefix     = var.aperture_s3_prefix
      aperture_s3_endpoint   = var.aperture_s3_endpoint
      aperture_s3_access_key = var.aperture_s3_access_key
      aperture_s3_secret_key = var.aperture_s3_secret_key
    }
    tailscale = {
      hostname  = "rj-gateway"
      state_dir = "/var/lib/rj-gateway/tsnet"
    }
  })
}

# =============================================================================
# Aperture Egress Service
# =============================================================================
#
# Tailscale Operator egress proxy: allows in-cluster pods (agents) to reach
# the Aperture AI gateway (ai.<tailnet>) via a K8s Service without needing
# direct tailnet access. The operator creates a proxy StatefulSet that
# tunnels traffic through the tailnet.
# =============================================================================

resource "kubernetes_service" "aperture_egress" {
  metadata {
    name      = "aperture"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "aperture-egress" })
    annotations = {
      "tailscale.com/tailnet-fqdn" = "${var.aperture_hostname}.${var.tailscale_tailnet}"
    }
  }

  spec {
    type          = "ExternalName"
    external_name = "placeholder.tailscale.svc.cluster.local"
  }

  depends_on = [helm_release.tailscale_operator]
}

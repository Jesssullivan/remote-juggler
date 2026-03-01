# =============================================================================
# Campaign Framework Resources
# =============================================================================
#
# ConfigMap for campaign definitions, mounted into the campaign-runner sidecar
# on the IronClaw pod. Campaign runner reads these at startup and evaluates
# triggers on an interval.
# =============================================================================

resource "kubernetes_config_map" "campaign_definitions" {
  metadata {
    name      = "campaign-definitions"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "campaign-runner" })
  }

  data = merge(
    { "index.json" = file("${path.module}/../../test/campaigns/index.json") },
    { for f in fileset("${path.module}/../../test/campaigns/gateway-direct", "*.json") : f => file("${path.module}/../../test/campaigns/gateway-direct/${f}") },
    { for f in fileset("${path.module}/../../test/campaigns/openclaw", "*.json") : f => file("${path.module}/../../test/campaigns/openclaw/${f}") },
    { for f in fileset("${path.module}/../../test/campaigns/hexstrike", "*.json") : f => file("${path.module}/../../test/campaigns/hexstrike/${f}") },
    { for f in fileset("${path.module}/../../test/campaigns/tinyclaw", "*.json") : f => file("${path.module}/../../test/campaigns/tinyclaw/${f}") },
    { for f in fileset("${path.module}/../../test/campaigns/cross-agent", "*.json") : f => file("${path.module}/../../test/campaigns/cross-agent/${f}") },
  )
}

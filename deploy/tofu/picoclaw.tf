# =============================================================================
# PicoClaw Agent + Adapter Sidecar
# =============================================================================
#
# 2-container pod:
#   1. picoclaw — PicoClaw-based agent (ghcr.io/tinyland-inc/picoclaw)
#   2. adapter  — campaign protocol bridge with tool proxy (PicoClaw lacks MCP)
#
# The adapter registers rj-gateway's 43 MCP tools in PicoClaw's native
# ToolRegistry format via the tools_proxy, bridging MCP tools into
# PicoClaw's native format.
# =============================================================================

resource "kubernetes_deployment" "picoclaw" {
  wait_for_rollout = false

  metadata {
    name      = "picoclaw-agent"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "picoclaw-agent", tier = "agent" })
  }

  spec {
    replicas = 1

    # Recreate strategy required: PVCs are ReadWriteOnce and cannot
    # multi-attach across nodes during rolling updates.
    strategy {
      type = "Recreate"
    }

    selector {
      match_labels = {
        app = "picoclaw-agent"
      }
    }

    template {
      metadata {
        labels = merge(local.labels, {
          app  = "picoclaw-agent"
          tier = "agent"
        })
      }

      spec {
        image_pull_secrets {
          name = kubernetes_secret.ghcr_pull.metadata[0].name
        }

        security_context {
          fs_group = 1000
        }

        # Workspace + state init: seed PVCs from image defaults on first boot only.
        # The state PVC at /home/picoclaw/.picoclaw shadows the baked-in config.json,
        # so we must copy the config into the PVC if it doesn't exist yet.
        init_container {
          name  = "workspace-init"
          image = var.picoclaw_image
          command = ["/bin/sh", "-c", <<-EOT
            # Always sync workspace: add new files without overwriting existing.
            cp -rn /workspace-defaults/* /workspace/ 2>/dev/null || true
            echo "Workspace synced from defaults (new files added, existing preserved)"
            # Update config.json if the image has a newer/larger config.
            IMG_CFG="/app/tinyland/config.json"
            if [ ! -f "$IMG_CFG" ]; then
              IMG_CFG="/home/picoclaw/.picoclaw-defaults/config.json"
            fi
            IMG_SIZE=$(wc -c < "$IMG_CFG" 2>/dev/null || echo 0)
            PVC_SIZE=$(wc -c < /state/config.json 2>/dev/null || echo 0)
            if [ ! -f /state/config.json ] || [ "$IMG_SIZE" -gt "$PVC_SIZE" ]; then
              cp "$IMG_CFG" /state/config.json 2>/dev/null || true
              echo "State config updated (image=$${IMG_SIZE}B > pvc=$${PVC_SIZE}B)"
            else
              echo "State config preserved (pvc=$${PVC_SIZE}B >= image=$${IMG_SIZE}B)"
            fi
          EOT
          ]
          volume_mount {
            name       = "workspace"
            mount_path = "/workspace"
          }
          volume_mount {
            name       = "state"
            mount_path = "/state"
          }
        }

        # PicoClaw agent container
        container {
          name  = "picoclaw"
          image = var.picoclaw_image

          env {
            name = "ANTHROPIC_API_KEY"
            value_from {
              secret_key_ref {
                name     = kubernetes_secret.agent_api_keys.metadata[0].name
                key      = "ANTHROPIC_API_KEY"
                optional = true
              }
            }
          }

          env {
            name  = "ANTHROPIC_BASE_URL"
            value = local.anthropic_direct_url
          }

          port {
            container_port = 18790
            name           = "agent"
          }

          volume_mount {
            name       = "workspace"
            mount_path = "/workspace"
          }

          volume_mount {
            name       = "state"
            mount_path = "/home/picoclaw/.picoclaw"
          }

          resources {
            requests = {
              cpu    = "100m"
              memory = "128Mi"
            }
            limits = {
              cpu    = "500m"
              memory = "256Mi"
            }
          }
        }

        # Adapter sidecar — bridges campaign protocol to PicoClaw's native API
        container {
          name  = "adapter"
          image = var.adapter_image

          args = [
            "--agent-type=picoclaw",
            "--agent-url=http://127.0.0.1:18790",
            "--listen-port=8080",
            "--gateway-url=http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080",
          ]

          port {
            container_port = 8080
            name           = "adapter"
          }

          resources {
            requests = {
              cpu    = "25m"
              memory = "32Mi"
            }
            limits = {
              cpu    = "100m"
              memory = "64Mi"
            }
          }
        }
        volume {
          name = "workspace"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.picoclaw_workspace.metadata[0].name
          }
        }

        volume {
          name = "state"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.picoclaw_state.metadata[0].name
          }
        }
      }
    }
  }

  depends_on = [helm_release.tailscale_operator]
}

# --- PVCs for PicoClaw persistent workspace and state ---

resource "kubernetes_persistent_volume_claim" "picoclaw_workspace" {
  wait_until_bound = false

  metadata {
    name      = "picoclaw-workspace"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "picoclaw-agent" })
  }

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = "2Gi"
      }
    }
  }
}

resource "kubernetes_persistent_volume_claim" "picoclaw_state" {
  wait_until_bound = false

  metadata {
    name      = "picoclaw-state"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "picoclaw-agent" })
  }

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = "1Gi"
      }
    }
  }
}

# ClusterIP Service for PicoClaw adapter (campaign runner dispatches here)
resource "kubernetes_service" "picoclaw" {
  metadata {
    name      = "picoclaw-agent"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "picoclaw-agent" })
  }

  spec {
    selector = {
      app = "picoclaw-agent"
    }

    port {
      port        = 8080
      target_port = 8080
      name        = "adapter"
    }

    type = "ClusterIP"
  }
}

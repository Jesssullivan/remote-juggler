# =============================================================================
# HexStrike-AI Pentest Agent + Adapter Sidecar
# =============================================================================
#
# 2-container pod:
#   1. hexstrike-ai — HexStrike-AI-based agent (ghcr.io/tinyland-inc/hexstrike-ai)
#   2. adapter      — campaign protocol bridge (Flask REST /api/command)
#
# HexStrike-AI exposes tools via a Flask REST API. The adapter dispatches
# campaign tool calls to POST /api/command on port 8888.
#
# Security: NET_RAW and NET_ADMIN for network security tools (nmap, etc.)
# Default: replicas=0 (dormant), scale up for engagements.
# =============================================================================

resource "kubernetes_persistent_volume_claim" "hexstrike_results" {
  wait_until_bound = false

  metadata {
    name      = "hexstrike-results"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "hexstrike-ai-agent" })
  }

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = var.hexstrike_results_storage_size
      }
    }
  }
}

resource "kubernetes_persistent_volume_claim" "hexstrike_workspace" {
  wait_until_bound = false

  metadata {
    name      = "hexstrike-workspace"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "hexstrike-ai-agent" })
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

resource "kubernetes_deployment" "hexstrike" {
  wait_for_rollout = false

  metadata {
    name      = "hexstrike-ai-agent"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels = merge(local.labels, {
      app      = "hexstrike-ai-agent"
      tier     = "agent"
      security = "pentest"
    })
  }

  spec {
    replicas = var.hexstrike_replicas

    selector {
      match_labels = {
        app = "hexstrike-ai-agent"
      }
    }

    template {
      metadata {
        labels = merge(local.labels, {
          app      = "hexstrike-ai-agent"
          tier     = "agent"
          security = "pentest"
        })
        annotations = {
          "tailscale.com/expose"   = "true"
          "tailscale.com/hostname" = "hexstrike"
        }
      }

      spec {
        image_pull_secrets {
          name = kubernetes_secret.ghcr_pull.metadata[0].name
        }

        security_context {
          fs_group = 10000
        }

        # Workspace init: seed PVC from image defaults on first boot only.
        init_container {
          name  = "workspace-init"
          image = var.hexstrike_ai_image
          command = ["/bin/sh", "-c", <<-EOT
            # Always sync workspace: add new files without overwriting existing.
            cp -rn /workspace-defaults/* /workspace/ 2>/dev/null || true
            echo "Workspace synced from defaults (new files added, existing preserved)"
          EOT
          ]
          volume_mount {
            name       = "workspace"
            mount_path = "/workspace"
          }
        }

        # HexStrike-AI agent container (standalone, based on 0x4m4/hexstrike-ai)
        container {
          name  = "hexstrike-ai"
          image = var.hexstrike_ai_image

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

          env {
            name  = "HEXSTRIKE_MODEL"
            value = var.hexstrike_model
          }

          env {
            name  = "RJ_GATEWAY_URL"
            value = "http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080"
          }

          env {
            name  = "MCP_SERVER_URL"
            value = "http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080/mcp"
          }

          env {
            name  = "GIT_AUTHOR_NAME"
            value = "rj-agent-bot[bot]"
          }
          env {
            name  = "GIT_AUTHOR_EMAIL"
            value = var.github_app_id != "" ? "${var.github_app_id}+rj-agent-bot[bot]@users.noreply.github.com" : "hexstrike@fuzzy-dev.tinyland.dev"
          }
          env {
            name  = "GIT_COMMITTER_NAME"
            value = "rj-agent-bot[bot]"
          }
          env {
            name  = "GIT_COMMITTER_EMAIL"
            value = var.github_app_id != "" ? "${var.github_app_id}+rj-agent-bot[bot]@users.noreply.github.com" : "hexstrike@fuzzy-dev.tinyland.dev"
          }
          env {
            name  = "GIT_SSH_COMMAND"
            value = "ssh -i /home/agent/.ssh/id_ed25519 -o StrictHostKeyChecking=no"
          }

          port {
            container_port = 8888
            name           = "flask"
          }

          volume_mount {
            name       = "workspace"
            mount_path = "/workspace"
          }

          volume_mount {
            name       = "results"
            mount_path = "/results"
          }

          volume_mount {
            name       = "ssh-keys"
            mount_path = "/home/agent/.ssh/id_ed25519"
            sub_path   = "hexstrike-id-ed25519"
            read_only  = true
          }

          resources {
            requests = {
              cpu    = "500m"
              memory = "1Gi"
            }
            limits = {
              cpu    = "2"
              memory = "2Gi"
            }
          }

          security_context {
            capabilities {
              add = ["NET_RAW", "NET_ADMIN"]
            }
          }
        }

        # Adapter sidecar — bridges campaign protocol to HexStrike's Flask REST API
        container {
          name  = "adapter"
          image = var.adapter_image

          args = [
            "--agent-type=hexstrike-ai",
            "--agent-url=http://localhost:8888",
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
            claim_name = kubernetes_persistent_volume_claim.hexstrike_workspace.metadata[0].name
          }
        }

        volume {
          name = "results"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.hexstrike_results.metadata[0].name
          }
        }

        volume {
          name = "ssh-keys"
          secret {
            secret_name  = kubernetes_secret.agent_ssh_keys.metadata[0].name
            default_mode = "0600"
            optional     = true
          }
        }
      }
    }
  }

  depends_on = [helm_release.tailscale_operator]
}

# ClusterIP Service for HexStrike-AI adapter (campaign runner dispatches here)
resource "kubernetes_service" "hexstrike" {
  metadata {
    name      = "hexstrike-ai-agent"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "hexstrike-ai-agent" })
  }

  spec {
    selector = {
      app = "hexstrike-ai-agent"
    }

    port {
      port        = 8080
      target_port = 8080
      name        = "adapter"
    }

    type = "ClusterIP"
  }
}

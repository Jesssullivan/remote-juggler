# =============================================================================
# IronClaw Agent + Adapter Sidecar + Campaign Runner
# =============================================================================
#
# 3-container pod:
#   1. ironclaw    — OpenClaw-based agent (ghcr.io/tinyland-inc/ironclaw)
#   2. adapter     — campaign protocol bridge (POST /campaign, GET /status)
#   3. campaign-runner — scheduler sidecar (reads ConfigMap, dispatches work)
#
# IronClaw uses mcporter bridge skill for MCP tool access to rj-gateway.
# LLM calls route through Aperture for identity-aware metering.
# =============================================================================

resource "kubernetes_deployment" "ironclaw" {
  wait_for_rollout = false

  metadata {
    name      = "ironclaw-agent"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "ironclaw-agent", tier = "agent" })
  }

  spec {
    replicas = 1

    selector {
      match_labels = {
        app = "ironclaw-agent"
      }
    }

    template {
      metadata {
        labels = merge(local.labels, {
          app  = "ironclaw-agent"
          tier = "agent"
        })
        annotations = {
          "tailscale.com/expose"   = "true"
          "tailscale.com/hostname" = "ironclaw"
        }
      }

      spec {
        image_pull_secrets {
          name = kubernetes_secret.ghcr_pull.metadata[0].name
        }

        security_context {
          fs_group = 10000
        }

        # Workspace + state init: seed PVCs from image defaults on first boot only.
        # The state PVC at /home/node/.openclaw shadows the baked-in openclaw.json,
        # so we must copy the config into the PVC if it doesn't exist yet.
        init_container {
          name  = "workspace-init"
          image = var.ironclaw_image
          command = ["/bin/sh", "-c", <<-EOT
            # Always sync workspace: add new files without overwriting existing.
            # cp -n (no-clobber) preserves user-modified files while adding new
            # workspace files, skills, and memory templates from updated images.
            cp -rn /workspace-defaults/* /workspace/ 2>/dev/null || true
            echo "Workspace synced from defaults (new files added, existing preserved)"
            # Update openclaw.json if the image has a newer/larger config.
            # Source is /app/tinyland/openclaw.json (baked into Dockerfile),
            # NOT /home/node/.openclaw/ which is the state PVC mount point.
            IMG_CFG="/app/tinyland/openclaw.json"
            IMG_SIZE=$(wc -c < "$IMG_CFG" 2>/dev/null || echo 0)
            PVC_SIZE=$(wc -c < /state/openclaw.json 2>/dev/null || echo 0)
            if [ ! -f /state/openclaw.json ] || [ "$IMG_SIZE" -gt "$PVC_SIZE" ]; then
              cp "$IMG_CFG" /state/openclaw.json
              echo "State config updated (image=${IMG_SIZE}B > pvc=${PVC_SIZE}B)"
            else
              echo "State config preserved (pvc=${PVC_SIZE}B >= image=${IMG_SIZE}B)"
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

        # Cron job seed: populate cron/jobs.json on first boot only.
        # OpenClaw cron jobs are stored in ~/.openclaw/cron/jobs.json, not in config.
        init_container {
          name  = "cron-seed"
          image = var.ironclaw_image
          command = ["/bin/sh", "-c", <<-EOT
            CRON_DIR="/state/cron"
            JOBS_FILE="$CRON_DIR/jobs.json"
            if [ -f "$JOBS_FILE" ]; then
              echo "Cron jobs already seeded, preserving"
              exit 0
            fi
            mkdir -p "$CRON_DIR"
            cat > "$JOBS_FILE" <<'SEED'
            [
              {
                "jobId": "reference-check",
                "name": "Reference Project Check",
                "enabled": true,
                "schedule": { "kind": "cron", "expr": "0 */6 * * *" },
                "sessionTarget": "isolated",
                "payload": { "kind": "systemEvent", "text": "Check openclaw/openclaw for new changes worth adopting. Compare key files with tinyland-inc/ironclaw main. Log findings to MEMORY.md." }
              },
              {
                "jobId": "health-report",
                "name": "Health Report",
                "enabled": true,
                "schedule": { "kind": "cron", "expr": "0 */4 * * *" },
                "sessionTarget": "isolated",
                "payload": { "kind": "systemEvent", "text": "Verify workspace files, check MEMORY.md staleness, test MCP tool connectivity. Log results to daily memory." }
              },
              {
                "jobId": "memory-maintenance",
                "name": "Memory Maintenance",
                "enabled": true,
                "schedule": { "kind": "cron", "expr": "0 1 * * *" },
                "sessionTarget": "isolated",
                "payload": { "kind": "systemEvent", "text": "Consolidate daily memory logs into MEMORY.md. Remove stale entries >14 days. Update TOOLS.md if tool behavior changed." }
              }
            ]
            SEED
            echo "Cron jobs seeded: reference-check (6h), health-report (4h), memory-maintenance (daily)"
          EOT
          ]
          volume_mount {
            name       = "state"
            mount_path = "/state"
          }
        }

        # IronClaw agent container (standalone, based on OpenClaw)
        container {
          name  = "ironclaw"
          image = var.ironclaw_image

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
            name  = "OPENCLAW_MODEL"
            value = var.ironclaw_model
          }

          env {
            name  = "MCP_SERVERS"
            value = jsonencode({ "rj-gateway" = { url = "http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080/mcp", transport = "http" } })
          }

          env {
            name  = "RJ_GATEWAY_URL"
            value = "http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080"
          }

          env {
            name  = "GIT_AUTHOR_NAME"
            value = "rj-agent-bot[bot]"
          }
          env {
            name  = "GIT_AUTHOR_EMAIL"
            value = var.github_app_id != "" ? "${var.github_app_id}+rj-agent-bot[bot]@users.noreply.github.com" : "ironclaw@fuzzy-dev.tinyland.dev"
          }
          env {
            name  = "GIT_COMMITTER_NAME"
            value = "rj-agent-bot[bot]"
          }
          env {
            name  = "GIT_COMMITTER_EMAIL"
            value = var.github_app_id != "" ? "${var.github_app_id}+rj-agent-bot[bot]@users.noreply.github.com" : "ironclaw@fuzzy-dev.tinyland.dev"
          }
          env {
            name  = "GIT_SSH_COMMAND"
            value = "ssh -i /home/agent/.ssh/id_ed25519 -o StrictHostKeyChecking=no"
          }

          env {
            name  = "BRAVE_API_KEY"
            value = var.brave_api_key
          }

          env {
            name = "OPENCLAW_GATEWAY_TOKEN"
            value_from {
              secret_key_ref {
                name     = kubernetes_secret.agent_api_keys.metadata[0].name
                key      = "OPENCLAW_GATEWAY_TOKEN"
                optional = true
              }
            }
          }

          port {
            container_port = 18789
            name           = "chat"
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
            mount_path = "/home/node/.openclaw"
          }

          volume_mount {
            name       = "ssh-keys"
            mount_path = "/home/agent/.ssh/id_ed25519"
            sub_path   = "ironclaw-id-ed25519"
            read_only  = true
          }

          resources {
            requests = {
              cpu    = "200m"
              memory = "768Mi"
            }
            limits = {
              cpu    = "1"
              memory = "2Gi"
            }
          }
        }

        # Adapter sidecar — bridges campaign protocol to IronClaw's OpenResponses API
        container {
          name  = "adapter"
          image = var.adapter_image

          args = [
            "--agent-type=ironclaw",
            "--agent-url=http://localhost:18789",
            "--listen-port=8080",
            "--gateway-url=http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080",
          ]

          env {
            name = "ADAPTER_AGENT_AUTH_TOKEN"
            value_from {
              secret_key_ref {
                name     = kubernetes_secret.agent_api_keys.metadata[0].name
                key      = "OPENCLAW_GATEWAY_TOKEN"
                optional = true
              }
            }
          }

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

        # Campaign runner sidecar
        dynamic "container" {
          for_each = var.campaign_runner_enabled ? [1] : []
          content {
            name  = "campaign-runner"
            image = var.campaign_runner_image

            args = [
              "--campaigns-dir=/etc/campaigns",
              "--gateway-url=http://rj-gateway.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080",
              "--ironclaw-url=http://localhost:8080",
              "--picoclaw-url=http://picoclaw-agent.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080",
              "--hexstrike-ai-url=http://hexstrike-ai-agent.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8080",
              "--interval=60s",
              "--api-port=8081",
            ]

            env {
              name = "GITHUB_TOKEN"
              value_from {
                secret_key_ref {
                  name     = kubernetes_secret.agent_api_keys.metadata[0].name
                  key      = "GITHUB_TOKEN"
                  optional = true
                }
              }
            }

            # GitHub App credentials for bot-attributed Discussions (issue #9).
            # When both are set, campaign runner generates JWT → installation token.
            env {
              name  = "GITHUB_APP_ID"
              value = var.github_app_id
            }

            env {
              name = "GITHUB_APP_PRIVATE_KEY"
              value_from {
                secret_key_ref {
                  name     = kubernetes_secret.agent_api_keys.metadata[0].name
                  key      = "GITHUB_APP_PRIVATE_KEY"
                  optional = true
                }
              }
            }

            env {
              name  = "GITHUB_REPO_OWNER"
              value = "tinyland-inc"
            }

            env {
              name  = "GITHUB_REPO_NAME"
              value = "remote-juggler"
            }

            port {
              container_port = 8081
              name           = "api"
            }

            volume_mount {
              name       = "campaigns"
              mount_path = "/etc/campaigns"
              read_only  = true
            }

            resources {
              requests = {
                cpu    = "50m"
                memory = "64Mi"
              }
              limits = {
                cpu    = "200m"
                memory = "128Mi"
              }
            }
          }
        }

        volume {
          name = "workspace"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.ironclaw_workspace.metadata[0].name
          }
        }

        volume {
          name = "state"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.ironclaw_state.metadata[0].name
          }
        }

        volume {
          name = "campaigns"
          config_map {
            name     = kubernetes_config_map.campaign_definitions.metadata[0].name
            optional = true
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

# --- PVCs for IronClaw persistent workspace and state ---

resource "kubernetes_persistent_volume_claim" "ironclaw_workspace" {
  wait_until_bound = false

  metadata {
    name      = "ironclaw-workspace"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "ironclaw-agent" })
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

resource "kubernetes_persistent_volume_claim" "ironclaw_state" {
  wait_until_bound = false

  metadata {
    name      = "ironclaw-state"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "ironclaw-agent" })
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

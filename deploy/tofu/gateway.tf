# =============================================================================
# rj-gateway — MCP Proxy + Composite Secret Resolver
# =============================================================================
#
# The gateway binary has tsnet embedded, so it joins the tailnet directly.
# No Tailscale sidecar needed — the operator annotation on the service is
# only for DNS registration if desired, but tsnet handles connectivity.
# =============================================================================

resource "kubernetes_persistent_volume_claim" "gateway_tsnet" {
  metadata {
    name      = "rj-gateway-tsnet"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "rj-gateway" })
  }

  wait_until_bound = false

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = "256Mi"
      }
    }
  }
}

resource "kubernetes_deployment" "gateway" {
  wait_for_rollout = false

  metadata {
    name      = "rj-gateway"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "rj-gateway" })
  }

  spec {
    replicas = 1

    # Recreate strategy required: tsnet PVC is ReadWriteOnce.
    strategy {
      type = "Recreate"
    }

    selector {
      match_labels = {
        app = "rj-gateway"
      }
    }

    template {
      metadata {
        labels = merge(local.labels, { app = "rj-gateway" })
      }

      spec {
        image_pull_secrets {
          name = kubernetes_secret.ghcr_pull.metadata[0].name
        }

        container {
          name  = "gateway"
          image = var.gateway_image

          args = ["--config=/etc/rj-gateway/config.json"]

          env {
            name = "TS_AUTHKEY"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.ts_auth.metadata[0].name
                key  = "TS_AUTHKEY"
              }
            }
          }

          env {
            name  = "TS_HOSTNAME"
            value = "rj-gateway"
          }

          env {
            name  = "TS_STATE_DIR"
            value = "/var/lib/tsnet"
          }

          port {
            container_port = 443
            name           = "https"
          }

          volume_mount {
            name       = "config"
            mount_path = "/etc/rj-gateway"
            read_only  = true
          }

          volume_mount {
            name       = "ts-state"
            mount_path = "/var/lib/tsnet"
          }

          volume_mount {
            name       = "shared-bin"
            mount_path = "/usr/local/bin/chapel"
          }

          env {
            name  = "RJ_GATEWAY_CHAPEL_BIN"
            value = "/usr/local/bin/chapel/remote-juggler"
          }

          # In-cluster HTTP listener for pod-to-pod communication (no TLS).
          env {
            name  = "RJ_GATEWAY_IN_CLUSTER_LISTEN"
            value = ":8080"
          }

          # Campaign runner API for cross-pod campaign orchestration.
          env {
            name  = "RJ_GATEWAY_CAMPAIGN_RUNNER_URL"
            value = "http://campaign-runner.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8081"
          }

          port {
            container_port = 8080
            name           = "http"
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

        # Init container: copy Chapel CLI binary to shared volume
        init_container {
          name  = "copy-cli"
          image = var.chapel_binary_image

          command = ["cp", "/usr/local/bin/remote-juggler", "/shared/remote-juggler"]

          volume_mount {
            name       = "shared-bin"
            mount_path = "/shared"
          }
        }

        volume {
          name = "config"
          secret {
            secret_name = kubernetes_secret.gateway_config.metadata[0].name
          }
        }

        volume {
          name = "ts-state"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.gateway_tsnet.metadata[0].name
          }
        }

        volume {
          name = "shared-bin"
          empty_dir {}
        }
      }
    }
  }

  depends_on = [helm_release.tailscale_operator]
}

# In-cluster Service for pod-to-pod communication.
# Agents (OpenClaw, HexStrike) reach the gateway via this service using plain
# HTTP on port 8080, avoiding the need for tailnet DNS resolution in every pod.
resource "kubernetes_service" "gateway_internal" {
  metadata {
    name      = "rj-gateway"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "rj-gateway" })
  }

  spec {
    selector = {
      app = "rj-gateway"
    }

    port {
      port        = 8080
      target_port = 8080
      name        = "http"
    }

    type = "ClusterIP"
  }
}

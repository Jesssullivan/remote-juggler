# =============================================================================
# Setec Secret Server
# =============================================================================
#
# Setec stores and serves secrets over the tailnet. The Tailscale Operator
# handles sidecar injection via the service annotation — no manual sidecar.
# =============================================================================

resource "kubernetes_persistent_volume_claim" "setec_data" {
  metadata {
    name      = "setec-data"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "setec" })
  }

  wait_until_bound = false

  spec {
    access_modes = ["ReadWriteOnce"]
    resources {
      requests = {
        storage = var.setec_storage_size
      }
    }
  }
}

resource "kubernetes_deployment" "setec" {
  wait_for_rollout = false

  metadata {
    name      = "setec"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "setec" })
  }

  spec {
    replicas = 1

    # Recreate strategy required: Setec's PVC is ReadWriteOnce.
    strategy {
      type = "Recreate"
    }

    selector {
      match_labels = {
        app = "setec"
      }
    }

    template {
      metadata {
        labels = merge(local.labels, { app = "setec" })
      }

      spec {
        image_pull_secrets {
          name = kubernetes_secret.ghcr_pull.metadata[0].name
        }

        security_context {
          fs_group = 10000
        }

        # Fix PVC ownership: existing data may be root-owned from before fs_group was set.
        init_container {
          name  = "fix-perms"
          image = "busybox:1.37"
          command = ["/bin/sh", "-c", "chown -R 10000:10000 /data"]

          security_context {
            run_as_user = 0
          }

          volume_mount {
            name       = "setec-data"
            mount_path = "/data"
          }
        }

        # Single container — Tailscale Operator injects the sidecar
        container {
          name  = "setec"
          image = var.setec_image

          args = [
            "server",
            "--state-dir=/data",
            "--hostname=setec",
            "--dev",
          ]

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
            name  = "TSNET_FORCE_LOGIN"
            value = "1"
          }

          volume_mount {
            name       = "setec-data"
            mount_path = "/data"
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

        volume {
          name = "setec-data"
          persistent_volume_claim {
            claim_name = kubernetes_persistent_volume_claim.setec_data.metadata[0].name
          }
        }
      }
    }
  }

  depends_on = [helm_release.tailscale_operator]
}

resource "kubernetes_service" "setec" {
  metadata {
    name      = "setec"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "setec" })

    annotations = {
      # Tailscale Operator: expose this service on the tailnet
      "tailscale.com/expose"   = "true"
      "tailscale.com/hostname" = "setec"
      "tailscale.com/tags"     = local.tailscale_tags.setec
    }
  }

  spec {
    selector = {
      app = "setec"
    }

    port {
      port        = 8443
      target_port = 8443
      name        = "https"
    }

    type = "ClusterIP"
  }

  depends_on = [helm_release.tailscale_operator]
}

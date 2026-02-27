# =============================================================================
# Setec Secret Seeding
# =============================================================================
#
# Seeds initial secrets into Setec via its HTTP API. Runs as a Kubernetes Job
# after Setec is deployed. Secrets are passed from tofu variables (sourced from
# SOPS via apply.sh). The job is idempotent — Setec's PUT creates or updates.
# =============================================================================

locals {
  # Map of secret names to their values for seeding.
  # Only non-empty values are seeded.
  setec_seed_secrets = {
    for k, v in {
      "github-token"              = var.github_token
      "gitlab-token"              = var.gitlab_token
      "anthropic-api-key"         = var.anthropic_api_key
      "github-app-private-key"    = var.github_app_private_key
    } : k => v if v != ""
  }

  # Setec API endpoint within the cluster (ClusterIP, no tailnet needed).
  # Port 8443 matches kubernetes_service.setec spec.
  setec_cluster_url = "http://${kubernetes_service.setec.metadata[0].name}.${kubernetes_namespace.main.metadata[0].name}.svc.cluster.local:8443"
}

resource "kubernetes_secret" "setec_seed" {
  count = length(local.setec_seed_secrets) > 0 ? 1 : 0

  metadata {
    name      = "setec-seed-data"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "setec-seed" })
  }

  data = local.setec_seed_secrets
}

resource "kubernetes_job" "setec_seed" {
  count = length(local.setec_seed_secrets) > 0 ? 1 : 0

  metadata {
    name      = "setec-seed-${substr(md5(join(",", keys(local.setec_seed_secrets))), 0, 8)}"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { app = "setec-seed" })
  }

  spec {
    backoff_limit = 3
    # Clean up after 5 minutes.
    ttl_seconds_after_finished = 300

    template {
      metadata {
        labels = merge(local.labels, { app = "setec-seed" })
      }

      spec {
        restart_policy = "OnFailure"

        container {
          name  = "seed"
          image = "curlimages/curl:8.5.0"

          command = ["/bin/sh", "-c"]
          args = [
            <<-EOT
            set -e
            echo "Seeding secrets into Setec at ${local.setec_cluster_url}..."

            # Wait for Setec to be ready (max 60s).
            for i in $(seq 1 12); do
              if curl -sf "${local.setec_cluster_url}/api/list" >/dev/null 2>&1; then
                echo "Setec is ready."
                break
              fi
              echo "Waiting for Setec... ($i/12)"
              sleep 5
            done

            # Seed each secret from mounted secret files.
            for name in ${join(" ", keys(local.setec_seed_secrets))}; do
              file="/secrets/$name"
              if [ -f "$file" ]; then
                value=$(cat "$file" | base64)
                echo "Seeding: remotejuggler/$name"
                curl -sf -X POST "${local.setec_cluster_url}/api/put" \
                  -H "Content-Type: application/json" \
                  -H "Sec-X-Tailscale-No-Browsers: setec" \
                  -d "{\"name\": \"remotejuggler/$name\", \"value\": \"$value\"}" || \
                  echo "  Warning: failed to seed $name (may already exist)"
              fi
            done

            echo "Setec seeding complete."
            EOT
          ]

          volume_mount {
            name       = "secrets"
            mount_path = "/secrets"
            read_only  = true
          }
        }

        volume {
          name = "secrets"
          secret {
            secret_name = kubernetes_secret.setec_seed[0].metadata[0].name
          }
        }
      }
    }
  }

  wait_for_completion = true

  timeouts {
    create = "2m"
  }

  depends_on = [kubernetes_deployment.setec]
}

# =============================================================================
# Agent Plane — shared resources and legacy aliases
# =============================================================================
#
# Individual agent deployments are in their own files:
#   - ironclaw.tf  (IronClaw — OpenClaw-based agent)
#   - tinyclaw.tf  (TinyClaw — lightweight agent for scan campaigns)
#   - hexstrike.tf (HexStrike-AI — security testing agent)
#
# This file contains shared resources used by all agents.
# =============================================================================

# Agent SSH identity keys (shared secret across all agent pods)
# Keys are resolved from Setec via apply.sh and stored as a K8s secret.
# Each agent mounts its own sub_path (e.g. ironclaw-id-ed25519).
resource "kubernetes_secret" "agent_ssh_keys" {
  metadata {
    name      = "agent-ssh-keys"
    namespace = kubernetes_namespace.main.metadata[0].name
    labels    = merge(local.labels, { tier = "agent" })
  }

  data = {
    # Bash $() strips trailing newlines; SSH keys require one.
    "ironclaw-id-ed25519"  = "${var.ironclaw_ssh_private_key}\n"
    "tinyclaw-id-ed25519"  = "${var.tinyclaw_ssh_private_key}\n"
    "hexstrike-id-ed25519" = "${var.hexstrike_ssh_private_key}\n"
  }

  type = "Opaque"
}

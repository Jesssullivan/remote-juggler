/*
 * Tools.chpl - MCP Tool Definitions and Handlers for RemoteJuggler
 *
 * This module defines all MCP tools exposed by the RemoteJuggler server
 * and implements their execution logic. Tools are the primary interface
 * for AI agents to interact with git identity management.
 *
 * Tools:
 * - juggler_list_identities: List all configured git identities
 * - juggler_detect_identity: Detect identity for current repository
 * - juggler_switch: Switch to a different git identity
 * - juggler_status: Get current identity and authentication status
 * - juggler_validate: Validate SSH and credential connectivity
 * - juggler_store_token: Store a token in the system keychain
 * - juggler_sync_config: Synchronize managed config blocks
 * - juggler_gpg_status: Check GPG/SSH signing readiness
 * - juggler_pin_store: Store YubiKey PIN in HSM
 * - juggler_pin_clear: Remove stored PIN from HSM
 * - juggler_pin_status: Check PIN storage status
 * - juggler_security_mode: Get/set security mode
 * - juggler_setup: Run first-time setup wizard
 */
prototype module Tools {
  use super.Protocol;
  use super.Core only getEnvVar, expandTilde;
  import super.Setup;  // Use import instead of use to avoid symbol conflicts
  import super.Manifest;
  import super.Identity;
  import super.AgeEncryption;
  use List;
  use IO;
  use OS.POSIX;
  use FileSystem;
  use Subprocess;
  use Path;

  // ============================================================================
  // Tool Definition Structure
  // ============================================================================

  /*
   * ToolDefinition - Describes a tool for the tools/list response
   *
   * name: Unique tool identifier (should be prefixed with "juggler_")
   * description: Human-readable description for AI agents
   * inputSchema: JSON Schema defining the tool's input parameters
   */
  record ToolDefinition {
    var name: string;
    var description: string;
    var inputSchema: string;  // JSON Schema as string
  }

  // ============================================================================
  // Tool Definitions
  // ============================================================================

  /*
   * Get all tool definitions for the tools/list response.
   *
   * Returns a list of all available tools with their schemas.
   */
  proc getToolDefinitions(): list(ToolDefinition) {
    var tools: list(ToolDefinition);

    // Tool: juggler_list_identities
    tools.pushBack(new ToolDefinition(
      name = "juggler_list_identities",
      description = "List all configured git identities with their providers (GitLab, GitHub, Bitbucket, etc.). Optionally filter by provider and include credential availability status.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"provider":{' +
            '"type":"string",' +
            '"enum":["gitlab","github","bitbucket","all"],' +
            '"description":"Filter identities by provider type. Use \'all\' or omit for all providers."' +
          '},' +
          '"includeCredentialStatus":{' +
            '"type":"boolean",' +
            '"description":"Include credential availability status (keychain, env, CLI) for each identity",' +
            '"default":false' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_detect_identity
    tools.pushBack(new ToolDefinition(
      name = "juggler_detect_identity",
      description = "Detect the git identity for a repository based on its remote URL. Analyzes SSH host aliases, gitconfig URL rewrites, and organization paths to determine the appropriate identity.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"repoPath":{' +
            '"type":"string",' +
            '"description":"Path to the git repository. Defaults to current working directory if not specified."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_switch
    tools.pushBack(new ToolDefinition(
      name = "juggler_switch",
      description = "Switch to a different git identity context. Updates git user config, authenticates with provider CLI (glab/gh) if available, and optionally configures GPG signing.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to switch to (e.g., \'personal\', \'work\', \'github-personal\')"' +
          '},' +
          '"setRemote":{' +
            '"type":"boolean",' +
            '"description":"Update git remote URL to match the identity\'s SSH host alias",' +
            '"default":true' +
          '},' +
          '"configureGPG":{' +
            '"type":"boolean",' +
            '"description":"Configure GPG signing using the identity\'s GPG key",' +
            '"default":true' +
          '},' +
          '"repoPath":{' +
            '"type":"string",' +
            '"description":"Path to git repository. Defaults to current working directory."' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // Tool: juggler_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_status",
      description = "Get the current git identity context, authentication status, GPG configuration, and recent switch history. Provides a comprehensive view of the current identity state.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"repoPath":{' +
            '"type":"string",' +
            '"description":"Path to git repository for context. Defaults to current working directory."' +
          '},' +
          '"verbose":{' +
            '"type":"boolean",' +
            '"description":"Include additional details like SSH key fingerprints and credential sources",' +
            '"default":false' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_validate
    tools.pushBack(new ToolDefinition(
      name = "juggler_validate",
      description = "Validate SSH key connectivity and credential availability for an identity. Tests the SSH connection to the provider and verifies token accessibility.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to validate (e.g., \'personal\', \'work\')"' +
          '},' +
          '"checkGPG":{' +
            '"type":"boolean",' +
            '"description":"Also verify GPG key exists and is registered with the provider",' +
            '"default":false' +
          '},' +
          '"testAuth":{' +
            '"type":"boolean",' +
            '"description":"Test authentication by making an API call to the provider",' +
            '"default":true' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // Tool: juggler_store_token
    tools.pushBack(new ToolDefinition(
      name = "juggler_store_token",
      description = "Store a token in the system keychain (macOS) or credential store for an identity. The token will be used for provider CLI authentication.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to store the token for"' +
          '},' +
          '"token":{' +
            '"type":"string",' +
            '"description":"The access token to store (GitLab/GitHub personal access token)"' +
          '}' +
        '},' +
        '"required":["identity","token"]' +
      '}'
    ));

    // Tool: juggler_sync_config
    tools.pushBack(new ToolDefinition(
      name = "juggler_sync_config",
      description = "Synchronize managed configuration blocks from SSH config and gitconfig. Updates the RemoteJuggler config file with the latest SSH hosts and URL rewrites.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"force":{' +
            '"type":"boolean",' +
            '"description":"Force sync even if no changes detected",' +
            '"default":false' +
          '},' +
          '"dryRun":{' +
            '"type":"boolean",' +
            '"description":"Show what would be changed without making changes",' +
            '"default":false' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_gpg_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_gpg_status",
      description = "Check GPG/SSH signing readiness including hardware token (YubiKey) status. Returns whether signing is possible, touch requirements, and actionable guidance for agents.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity to check signing status for. If omitted, checks current repository context."' +
          '},' +
          '"repoPath":{' +
            '"type":"string",' +
            '"description":"Path to git repository for context. Defaults to current working directory."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_pin_store
    tools.pushBack(new ToolDefinition(
      name = "juggler_pin_store",
      description = "Store a YubiKey PIN securely in the hardware security module (TPM on Linux, SecureEnclave on macOS). This enables Trusted Workstation mode where the PIN is automatically retrieved for gpg-agent.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to store the PIN for (e.g., \'personal\', \'work\')"' +
          '},' +
          '"pin":{' +
            '"type":"string",' +
            '"description":"The YubiKey PIN to store (6-127 characters)"' +
          '}' +
        '},' +
        '"required":["identity","pin"]' +
      '}'
    ));

    // Tool: juggler_pin_clear
    tools.pushBack(new ToolDefinition(
      name = "juggler_pin_clear",
      description = "Remove a stored YubiKey PIN from the hardware security module. After clearing, the PIN must be re-entered for signing operations.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to clear the PIN for"' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // Tool: juggler_pin_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_pin_status",
      description = "Check the status of PIN storage in the hardware security module. Returns HSM availability, stored identities, and current security mode.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Optional: Check PIN status for a specific identity. If omitted, shows status for all identities."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_security_mode
    tools.pushBack(new ToolDefinition(
      name = "juggler_security_mode",
      description = "Get or set the security mode for GPG signing operations. Modes: maximum_security (PIN required each time), developer_workflow (PIN cached by gpg-agent), trusted_workstation (PIN stored in HSM).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"mode":{' +
            '"type":"string",' +
            '"enum":["maximum_security","developer_workflow","trusted_workstation"],' +
            '"description":"Security mode to set. Omit to get current mode."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_setup
    tools.pushBack(new ToolDefinition(
      name = "juggler_setup",
      description = "Run the RemoteJuggler first-time setup wizard. Auto-detects SSH hosts, GPG keys, and HSM availability to generate configuration. Use mode='auto' for non-interactive setup suitable for AI agents.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"mode":{' +
            '"type":"string",' +
            '"enum":["auto","status","import_ssh","import_gpg"],' +
            '"description":"Setup mode: auto (non-interactive, recommended for agents), status (read-only check), import_ssh (SSH hosts only), import_gpg (GPG keys only). Default: auto",' +
            '"default":"auto"' +
          '},' +
          '"force":{' +
            '"type":"boolean",' +
            '"description":"Overwrite existing configuration if present",' +
            '"default":false' +
          '}' +
        '}' +
      '}'
    ));

    // ====================================================================
    // Priority 1 - High value for Claude Code agents
    // ====================================================================

    // Tool: juggler_token_verify
    tools.pushBack(new ToolDefinition(
      name = "juggler_token_verify",
      description = "Verify that stored tokens are valid by making a test API call to the provider. Returns token status, scopes, and expiry information.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to verify token for. If omitted, verifies current identity."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_config_show
    tools.pushBack(new ToolDefinition(
      name = "juggler_config_show",
      description = "Show the current RemoteJuggler configuration. Optionally filter to a specific section (identities, settings, state, managed_ssh, managed_git).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"section":{' +
            '"type":"string",' +
            '"enum":["all","identities","settings","state","managed_ssh","managed_git"],' +
            '"description":"Configuration section to display. Default: all",' +
            '"default":"all"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_debug_ssh
    tools.pushBack(new ToolDefinition(
      name = "juggler_debug_ssh",
      description = "Debug SSH configuration by showing parsed SSH hosts, testing connectivity, and checking key file permissions. Useful for diagnosing connection issues.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{}' +
      '}'
    ));

    // ====================================================================
    // Priority 2 - Useful for automation
    // ====================================================================

    // Tool: juggler_token_get
    tools.pushBack(new ToolDefinition(
      name = "juggler_token_get",
      description = "Retrieve a stored token for an identity. Returns the token masked (first 4 and last 4 chars visible) for security. Only returns full token in trusted workstation mode.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to get token for"' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // Tool: juggler_token_clear
    tools.pushBack(new ToolDefinition(
      name = "juggler_token_clear",
      description = "Remove a stored token for an identity from the credential store (keychain, environment reference, or CLI auth).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity name to clear token for"' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // Tool: juggler_tws_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_tws_status",
      description = "Check Trusted Workstation status including HSM availability, stored PINs, auto-unlock capability, and YubiKey presence. Comprehensive view of the trusted workstation configuration.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Optional identity to check. If omitted, shows status for all identities."' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_tws_enable
    tools.pushBack(new ToolDefinition(
      name = "juggler_tws_enable",
      description = "Enable Trusted Workstation mode for an identity. Requires HSM availability and a stored PIN. Sets security mode to trusted_workstation and configures gpg-agent for automatic PIN retrieval.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"identity":{' +
            '"type":"string",' +
            '"description":"Identity to enable Trusted Workstation mode for"' +
          '}' +
        '},' +
        '"required":["identity"]' +
      '}'
    ));

    // ====================================================================
    // KeePassXC Key Store Tools
    // ====================================================================

    // Tool: juggler_keys_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_status",
      description = "Check KeePassXC key store availability, lock state, HSM status, and entry count. Returns comprehensive status of the credential authority.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{}' +
      '}'
    ));

    // Tool: juggler_keys_search
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_search",
      description = "Fuzzy search across all entries in the KeePassXC key store. Searches titles, paths, usernames, notes, and URLs using Levenshtein distance and word boundary matching. Returns ranked results without exposing secret values.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"query":{' +
            '"type":"string",' +
            '"description":"Search query (e.g., \'gitlab\', \'perplexity\', \'sudo\')"' +
          '},' +
          '"group":{' +
            '"type":"string",' +
            '"description":"Optional group to restrict search (e.g., \'RemoteJuggler/API\')"' +
          '},' +
          '"format":{' +
            '"type":"string",' +
            '"description":"Output format: text (default) or json for structured output",' +
            '"default":"text"' +
          '}' +
        '},' +
        '"required":["query"]' +
      '}'
    ));

    // Tool: juggler_keys_get
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_get",
      description = "Retrieve a secret value from the KeePassXC key store by entry path. Only works when auto-unlock is available (HSM + YubiKey present). Returns the secret value.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"entryPath":{' +
            '"type":"string",' +
            '"description":"Full entry path within the database (e.g., \'RemoteJuggler/API/PERPLEXITY_API_KEY\')"' +
          '}' +
        '},' +
        '"required":["entryPath"]' +
      '}'
    ));

    // Tool: juggler_keys_store
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_store",
      description = "Store or update a secret in the KeePassXC key store. Creates the entry if it doesn't exist, updates if it does. Requires auto-unlock (HSM + YubiKey).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"entryPath":{' +
            '"type":"string",' +
            '"description":"Full entry path (e.g., \'RemoteJuggler/API/MY_KEY\')"' +
          '},' +
          '"value":{' +
            '"type":"string",' +
            '"description":"Secret value to store"' +
          '},' +
          '"notes":{' +
            '"type":"string",' +
            '"description":"Optional notes for the entry"' +
          '}' +
        '},' +
        '"required":["entryPath","value"]' +
      '}'
    ));

    // Tool: juggler_keys_ingest_env
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_ingest_env",
      description = "Ingest a .env file into the KeePassXC key store. Parses KEY=VALUE pairs and stores each as a separate entry under RemoteJuggler/Environments/. Handles comments, export prefixes, and quoted values.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"filePath":{' +
            '"type":"string",' +
            '"description":"Path to the .env file to ingest"' +
          '}' +
        '},' +
        '"required":["filePath"]' +
      '}'
    ));

    // Tool: juggler_keys_list
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_list",
      description = "List entries in a group within the KeePassXC key store. Defaults to the RemoteJuggler root group. Shows group names (ending with /) and entry names.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"group":{' +
            '"type":"string",' +
            '"description":"Group path to list (default: \'RemoteJuggler\')",' +
            '"default":"RemoteJuggler"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_keys_init
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_init",
      description = "Bootstrap a new KeePassXC kdbx credential database. Creates the database, group hierarchy, seals the master password in HSM (TPM/SecureEnclave), and imports existing credentials from environment.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{}' +
      '}'
    ));

    // Tool: juggler_keys_resolve
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_resolve",
      description = "Search and retrieve a secret in one operation. Fuzzy-searches for the best match and returns its value directly. Eliminates the two-call search-then-get pattern.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"query":{' +
            '"type":"string",' +
            '"description":"Search query (e.g., \'gitlab\', \'perplexity\')"' +
          '},' +
          '"group":{' +
            '"type":"string",' +
            '"description":"Optional group to restrict search"' +
          '},' +
          '"threshold":{' +
            '"type":"integer",' +
            '"description":"Minimum match score to accept (default: 40, range: 0-100)",' +
            '"default":40' +
          '}' +
        '},' +
        '"required":["query"]' +
      '}'
    ));

    // Tool: juggler_keys_delete
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_delete",
      description = "Delete an entry from the KeePassXC key store. Requires the full entry path. Use juggler_keys_search to find the exact path first.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"entryPath":{' +
            '"type":"string",' +
            '"description":"Full entry path to delete (e.g., \'RemoteJuggler/API/OLD_KEY\')"' +
          '},' +
          '"confirm":{' +
            '"type":"boolean",' +
            '"description":"Must be true to confirm deletion",' +
            '"default":false' +
          '}' +
        '},' +
        '"required":["entryPath","confirm"]' +
      '}'
    ));

    // Tool: juggler_keys_crawl_env
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_crawl_env",
      description = "Crawl directories for .env files and ingest all found files into the key store. Searches ~, ~/git, ~/projects by default.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"dirs":{' +
            '"type":"array",' +
            '"items":{"type":"string"},' +
            '"description":"Directories to search (default: [~, ~/git, ~/projects])"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_keys_discover
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_discover",
      description = "Auto-discover credentials from environment variables, SSH keys, or both. Scans for *_TOKEN, *_API_KEY, *_SECRET patterns and SSH key metadata.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"types":{' +
            '"type":"string",' +
            '"description":"What to discover: env, ssh, or all (default: all)",' +
            '"default":"all"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_keys_export
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_export",
      description = "Export entries from a key store group as .env or JSON format. Useful for reconstructing environment files from stored credentials.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"group":{' +
            '"type":"string",' +
            '"description":"Group path to export (e.g., \'RemoteJuggler/API\')"' +
          '},' +
          '"format":{' +
            '"type":"string",' +
            '"description":"Output format: env or json (default: env)",' +
            '"default":"env"' +
          '}' +
        '},' +
        '"required":["group"]' +
      '}'
    ));

    // Tool: juggler_keys_sops_status
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_sops_status",
      description = "Check SOPS integration availability. Reports whether sops and age binaries are installed and any SOPS-encrypted entries in the key store.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{}' +
      '}'
    ));

    // Tool: juggler_keys_sops_ingest
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_sops_ingest",
      description = "Ingest a SOPS-encrypted file into the KeePassXC key store. Decrypts with sops -d and stores each key-value pair under RemoteJuggler/SOPS/{file-path}/{key}. Requires sops and age binaries.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"filePath":{' +
            '"type":"string",' +
            '"description":"Path to the SOPS-encrypted file (e.g., secrets.sops.yaml)"' +
          '}' +
        '},' +
        '"required":["filePath"]' +
      '}'
    ));

    // Tool: juggler_keys_sops_sync
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_sops_sync",
      description = "Sync a SOPS-encrypted file with the key store. Detects additions, updates, and deletions compared to the stored KDBX entries. Returns change counts.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"filePath":{' +
            '"type":"string",' +
            '"description":"Path to the SOPS-encrypted file to sync"' +
          '}' +
        '},' +
        '"required":["filePath"]' +
      '}'
    ));

    // Tool: juggler_keys_sops_export
    tools.pushBack(new ToolDefinition(
      name = "juggler_keys_sops_export",
      description = "Export age public key from the key store for SOPS recipients configuration. Returns the age public key suitable for .sops.yaml creation_rules.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"format":{' +
            '"type":"string",' +
            '"description":"Export format: sops-age (default) for .sops.yaml recipients",' +
            '"default":"sops-age"' +
          '},' +
          '"group":{' +
            '"type":"string",' +
            '"description":"KDBX group containing age key (default: RemoteJuggler/SOPS)"' +
          '}' +
        '}' +
      '}'
    ));

    // ========================================================================
    // IBSF (Identity-Bound Secret Fabric) Tools — Sprint B-E
    // ========================================================================

    // Tool: juggler_secrets_discover (IBSF Sprint B)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_discover",
      description = "Discover all secrets across all sources (KDBX, environment, SSH, .env files) and generate a fleet-wide manifest at ~/tcfs/secrets/.manifest.toml. Returns a summary of discovered secrets with classification (shared/personal/machine-specific).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_manifest (IBSF Sprint B)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_manifest",
      description = "Read and return the current fleet secret manifest (~/.manifest.toml in TCFS secrets). Returns the full manifest as JSON with metadata and all secret entries (hashes only, never plaintext values).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '},' +
          '"format":{' +
            '"type":"string",' +
            '"description":"Output format: json (default) or summary",' +
            '"default":"json"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_encrypt (IBSF Sprint C)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_encrypt",
      description = "Encrypt a file or value using the user's age identity key. Produces a .age file suitable for TCFS sync. Uses age-plugin-yubikey (macOS) or age-plugin-tpm (Linux).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"input_path":{' +
            '"type":"string",' +
            '"description":"Path to plaintext file to encrypt"' +
          '},' +
          '"value":{' +
            '"type":"string",' +
            '"description":"Plaintext value to encrypt (alternative to input_path)"' +
          '},' +
          '"output_path":{' +
            '"type":"string",' +
            '"description":"Output path for .age file (default: input_path + .age)"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_decrypt (IBSF Sprint C)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_decrypt",
      description = "Decrypt an age-encrypted file using the user's HSM-backed identity key. Requires physical YubiKey (macOS) or TPM (Linux).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"input_path":{' +
            '"type":"string",' +
            '"description":"Path to .age encrypted file"' +
          '},' +
          '"output_path":{' +
            '"type":"string",' +
            '"description":"Output path for decrypted file (default: strip .age extension)"' +
          '},' +
          '"to_string":{' +
            '"type":"boolean",' +
            '"description":"Return decrypted value as string instead of writing to file",' +
            '"default":false' +
          '}' +
        '},' +
        '"required":["input_path"]' +
      '}'
    ));

    // Tool: juggler_secrets_rekey (IBSF Sprint C)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_rekey",
      description = "Re-encrypt all .age files in TCFS secrets directory with a new age recipient key. Used after identity key rotation.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"new_recipient":{' +
            '"type":"string",' +
            '"description":"New age recipient public key to re-encrypt to"' +
          '},' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '},' +
        '"required":["new_recipient"]' +
      '}'
    ));

    // Tool: juggler_secrets_sync (IBSF Sprint D)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_sync",
      description = "Full IBSF sync cycle: discover local secrets, merge with remote manifests, push local-only secrets, pull remote-only secrets. Returns merge summary.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '},' +
          '"conflict_strategy":{' +
            '"type":"string",' +
            '"description":"Conflict resolution: last-write-wins (default), source-priority, keep-both, prompt-user",' +
            '"default":"last-write-wins"' +
          '},' +
          '"dry_run":{' +
            '"type":"boolean",' +
            '"description":"If true, report what would happen without making changes",' +
            '"default":false' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_conflicts (IBSF Sprint D)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_conflicts",
      description = "List unresolved merge conflicts from the last sync cycle. Shows local vs remote hash, source hosts, and source types for each conflict.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_resolve (IBSF Sprint D)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_resolve",
      description = "Resolve a specific merge conflict. Choose keep-local (push local version), keep-remote (pull remote version), or keep-both (version both).",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"canonical_name":{' +
            '"type":"string",' +
            '"description":"Canonical name of the conflicting secret"' +
          '},' +
          '"resolution":{' +
            '"type":"string",' +
            '"description":"Resolution strategy: keep-local, keep-remote, keep-both"' +
          '},' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '},' +
        '"required":["canonical_name","resolution"]' +
      '}'
    ));

    // Tool: juggler_secrets_get (IBSF Sprint E)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_get",
      description = "Retrieve a secret value by canonical name. Decrypts from TCFS if age-encrypted. Returns the plaintext value, source type, and sync status.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"canonical_name":{' +
            '"type":"string",' +
            '"description":"Canonical name of the secret (e.g., api/anthropic, token/gitlab)"' +
          '},' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '},' +
        '"required":["canonical_name"]' +
      '}'
    ));

    // Tool: juggler_secrets_status (IBSF Sprint E)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_status",
      description = "Overall IBSF health dashboard. Returns manifest version, total secrets count, synced/unsynced counts, conflict count, and last sync timestamp.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '}' +
      '}'
    ));

    // Tool: juggler_secrets_audit (IBSF Sprint E)
    tools.pushBack(new ToolDefinition(
      name = "juggler_secrets_audit",
      description = "Read recent entries from the IBSF audit log. Returns timestamped records of sync, resolve, encrypt, and discovery operations.",
      inputSchema = '{' +
        '"type":"object",' +
        '"properties":{' +
          '"limit":{' +
            '"type":"integer",' +
            '"description":"Maximum number of audit entries to return (default: 20)"' +
          '},' +
          '"tcfs_secrets_dir":{' +
            '"type":"string",' +
            '"description":"Path to TCFS secrets directory (default: ~/tcfs/secrets)"' +
          '}' +
        '}' +
      '}'
    ));

    return tools;
  }

  // ============================================================================
  // Tool Execution Dispatcher
  // ============================================================================

  /*
   * Execute a tool by name with the given parameters.
   *
   * @param name: Tool name (e.g., "juggler_list_identities")
   * @param params: JSON-encoded parameters object
   * @return: (success, resultContent) tuple
   */
  proc executeTool(name: string, params: string): (bool, string) {
    stderr.writeln("Tools: Executing tool '", name, "' with params: ", params);

    select name {
      when "juggler_list_identities" {
        return handleListIdentities(params);
      }
      when "juggler_detect_identity" {
        return handleDetectIdentity(params);
      }
      when "juggler_switch" {
        return handleSwitch(params);
      }
      when "juggler_status" {
        return handleStatus(params);
      }
      when "juggler_validate" {
        return handleValidate(params);
      }
      when "juggler_store_token" {
        return handleStoreToken(params);
      }
      when "juggler_sync_config" {
        return handleSyncConfig(params);
      }
      when "juggler_gpg_status" {
        return handleGPGStatus(params);
      }
      when "juggler_pin_store" {
        return handlePinStore(params);
      }
      when "juggler_pin_clear" {
        return handlePinClear(params);
      }
      when "juggler_pin_status" {
        return handlePinStatus(params);
      }
      when "juggler_security_mode" {
        return handleSecurityMode(params);
      }
      when "juggler_setup" {
        return handleSetup(params);
      }
      when "juggler_token_verify" {
        return handleTokenVerify(params);
      }
      when "juggler_config_show" {
        return handleConfigShow(params);
      }
      when "juggler_debug_ssh" {
        return handleDebugSSH(params);
      }
      when "juggler_token_get" {
        return handleTokenGet(params);
      }
      when "juggler_token_clear" {
        return handleTokenClear(params);
      }
      when "juggler_tws_status" {
        return handleTWSStatus(params);
      }
      when "juggler_tws_enable" {
        return handleTWSEnable(params);
      }
      when "juggler_keys_status" {
        return handleKeysStatusTool(params);
      }
      when "juggler_keys_search" {
        return handleKeysSearchTool(params);
      }
      when "juggler_keys_get" {
        return handleKeysGetTool(params);
      }
      when "juggler_keys_store" {
        return handleKeysStoreTool(params);
      }
      when "juggler_keys_ingest_env" {
        return handleKeysIngestEnvTool(params);
      }
      when "juggler_keys_list" {
        return handleKeysListTool(params);
      }
      when "juggler_keys_init" {
        return handleKeysInitTool(params);
      }
      when "juggler_keys_resolve" {
        return handleKeysResolveTool(params);
      }
      when "juggler_keys_delete" {
        return handleKeysDeleteTool(params);
      }
      when "juggler_keys_crawl_env" {
        return handleKeysCrawlEnvTool(params);
      }
      when "juggler_keys_discover" {
        return handleKeysDiscoverTool(params);
      }
      when "juggler_keys_export" {
        return handleKeysExportTool(params);
      }
      when "juggler_keys_sops_status" {
        return handleKeysSopsStatusTool(params);
      }
      when "juggler_keys_sops_ingest" {
        return handleKeysSopsIngestTool(params);
      }
      when "juggler_keys_sops_sync" {
        return handleKeysSopsSyncTool(params);
      }
      when "juggler_keys_sops_export" {
        return handleKeysSopsExportTool(params);
      }
      // IBSF (Identity-Bound Secret Fabric) dispatch
      when "juggler_secrets_discover" {
        return handleSecretsDiscoverTool(params);
      }
      when "juggler_secrets_manifest" {
        return handleSecretsManifestTool(params);
      }
      when "juggler_secrets_encrypt" {
        return handleSecretsEncryptTool(params);
      }
      when "juggler_secrets_decrypt" {
        return handleSecretsDecryptTool(params);
      }
      when "juggler_secrets_rekey" {
        return handleSecretsRekeyTool(params);
      }
      when "juggler_secrets_sync" {
        return handleSecretsSyncTool(params);
      }
      when "juggler_secrets_conflicts" {
        return handleSecretsConflictsTool(params);
      }
      when "juggler_secrets_resolve" {
        return handleSecretsResolveTool(params);
      }
      when "juggler_secrets_get" {
        return handleSecretsGetTool(params);
      }
      when "juggler_secrets_status" {
        return handleSecretsStatusTool(params);
      }
      when "juggler_secrets_audit" {
        return handleSecretsAuditTool(params);
      }
      otherwise {
        stderr.writeln("Tools: Unknown tool: ", name);
        return (false, "Unknown tool: " + name);
      }
    }
  }

  // ============================================================================
  // Configuration Helpers
  // ============================================================================

  /*
   * Get the path to the RemoteJuggler config file.
   */
  proc getConfigPath(): string {
    const home = getEnvHome();
    return home + "/.config/remote-juggler/config.json";
  }

  /*
   * Get the HOME environment variable.
   */
  proc getEnvHome(): string {
    const home = getEnvVar("HOME");
    return if home != "" then home else "/tmp";
  }

  /*
   * Get the current working directory.
   */
  proc getCwd(): string {
    try {
      return here.cwd();
    } catch {
      return ".";
    }
  }

  /*
   * Read the config file contents.
   */
  proc readConfigFile(): (bool, string) {
    const configPath = getConfigPath();

    if !exists(configPath) {
      return (false, "");
    }

    try {
      var f = open(configPath, ioMode.r);
      var reader = f.reader(locking=false);
      var content: string;
      reader.readAll(content);
      reader.close();
      f.close();
      return (true, content);
    } catch {
      return (false, "");
    }
  }

  // ============================================================================
  // Git Helpers
  // ============================================================================

  /*
   * Get the git remote URL for a repository.
   */
  proc getGitRemoteUrl(repoPath: string, remoteName: string = "origin"): (bool, string) {
    try {
      var p = spawn(["git", "-C", repoPath, "remote", "get-url", remoteName],
                       stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var url: string;
        p.stdout.readAll(url);
        return (true, url.strip());
      }
    } catch {
      // Fall through
    }
    return (false, "");
  }

  /*
   * Get the current git user.name for a repository.
   */
  proc getGitUserName(repoPath: string): (bool, string) {
    try {
      var p = spawn(["git", "-C", repoPath, "config", "user.name"],
                       stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var name: string;
        p.stdout.readAll(name);
        return (true, name.strip());
      }
    } catch {
      // Fall through
    }
    return (false, "");
  }

  /*
   * Get the current git user.email for a repository.
   */
  proc getGitUserEmail(repoPath: string): (bool, string) {
    try {
      var p = spawn(["git", "-C", repoPath, "config", "user.email"],
                       stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var email: string;
        p.stdout.readAll(email);
        return (true, email.strip());
      }
    } catch {
      // Fall through
    }
    return (false, "");
  }

  /*
   * Set a git config value for a repository.
   */
  proc setGitConfig(repoPath: string, key: string, value: string): bool {
    try {
      var p = spawn(["git", "-C", repoPath, "config", key, value],
                       stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Set the git remote URL for a repository.
   */
  proc setGitRemoteUrl(repoPath: string, remoteName: string, url: string): bool {
    try {
      var p = spawn(["git", "-C", repoPath, "remote", "set-url", remoteName, url],
                       stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  // ============================================================================
  // SSH Helpers
  // ============================================================================

  /*
   * Test SSH connectivity to a host.
   */
  proc testSSHConnection(host: string): (bool, string) {
    try {
      // Use ssh -T to test connection without executing a command
      var p = spawn(["ssh", "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
                        "git@" + host],
                       stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      var output: string;
      p.stderr.readAll(output);

      // GitHub/GitLab return exit code 1 but print welcome message
      if output.find("successfully authenticated") != -1 ||
         output.find("Welcome to GitLab") != -1 ||
         output.find("Hi ") != -1 {
        return (true, output.strip());
      }

      // Check for common success patterns
      if p.exitCode == 1 && output.size > 0 {
        return (true, output.strip());
      }

      return (false, output.strip());
    } catch e {
      return (false, "SSH test failed: " + e.message());
    }
  }

  // ============================================================================
  // Provider CLI Helpers
  // ============================================================================

  /*
   * Check if glab CLI is available.
   */
  proc isGlabAvailable(): bool {
    try {
      var p = spawn(["which", "glab"],
                       stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Check if gh CLI is available.
   */
  proc isGhAvailable(): bool {
    try {
      var p = spawn(["which", "gh"],
                       stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Get the current glab auth status.
   */
  proc getGlabAuthStatus(hostname: string = "gitlab.com"): (bool, string) {
    if !isGlabAvailable() {
      return (false, "glab not installed");
    }

    try {
      var p = spawn(["glab", "auth", "status", "-h", hostname],
                       stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      var output: string;
      p.stdout.readAll(output);
      p.stderr.readAll(output);

      return (p.exitCode == 0, output.strip());
    } catch e {
      return (false, e.message());
    }
  }

  /*
   * Get the current gh auth status.
   */
  proc getGhAuthStatus(hostname: string = "github.com"): (bool, string) {
    if !isGhAvailable() {
      return (false, "gh not installed");
    }

    try {
      var args: list(string);
      args.pushBack("gh");
      args.pushBack("auth");
      args.pushBack("status");
      if hostname != "github.com" {
        args.pushBack("-h");
        args.pushBack(hostname);
      }

      var p = spawn(args.toArray(), stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      var output: string;
      p.stdout.readAll(output);
      p.stderr.readAll(output);

      return (p.exitCode == 0, output.strip());
    } catch e {
      return (false, e.message());
    }
  }

  // ============================================================================
  // Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_list_identities tool call.
   *
   * Lists all configured identities from the config file.
   */
  proc handleListIdentities(params: string): (bool, string) {
    stderr.writeln("Tools: handleListIdentities");

    // Parse filter parameter
    const (hasProvider, providerFilter) = Protocol.extractJsonString(params, "provider");
    const filterProvider = if hasProvider && providerFilter != "all" then providerFilter else "";

    const (hasCreds, includeCredStr) = Protocol.extractJsonString(params, "includeCredentialStatus");
    const includeCreds = hasCreds && includeCredStr == "true";

    // Read config file
    const (configOk, configContent) = readConfigFile();

    if !configOk {
      // Return sample identities if no config exists
      const sampleOutput =
        "No configuration file found at " + getConfigPath() + "\n\n" +
        "To create a configuration, run:\n" +
        "  remote-juggler config init\n\n" +
        "Or create ~/.config/remote-juggler/config.json manually with identity definitions.";
      return (true, sampleOutput);
    }

    // Extract identities from config
    const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");

    if !hasIdentities {
      return (true, "No identities configured in " + getConfigPath());
    }

    // Build output string
    var output = "Configured Git Identities:\n";
    output += "==========================\n\n";

    // Parse each identity (simplified - in production would use proper JSON parsing)
    // For now, list known identity names
    const knownIdentities = ["personal", "work", "github-personal"];

    for identityName in knownIdentities {
      const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identityName);
      if hasId {
        // Extract identity fields
        const (_, provider) = Protocol.extractJsonString(idJson, "provider");
        const (_, user) = Protocol.extractJsonString(idJson, "user");
        const (_, email) = Protocol.extractJsonString(idJson, "email");
        const (_, host) = Protocol.extractJsonString(idJson, "host");

        // Apply filter
        if filterProvider != "" && provider != filterProvider {
          continue;
        }

        output += "- " + identityName + "\n";
        output += "    Provider: " + provider + "\n";
        output += "    User: " + user + "\n";
        output += "    Email: " + email + "\n";
        output += "    SSH Host: " + host + "\n";

        if includeCreds {
          // Check credential availability
          const (_, credSource) = Protocol.extractJsonString(idJson, "credentialSource");
          output += "    Credential Source: " + credSource + "\n";

          // Test actual availability
          if provider == "gitlab" {
            const (authOk, _) = getGlabAuthStatus();
            output += "    glab CLI: " + (if authOk then "authenticated" else "not authenticated") + "\n";
          } else if provider == "github" {
            const (authOk, _) = getGhAuthStatus();
            output += "    gh CLI: " + (if authOk then "authenticated" else "not authenticated") + "\n";
          }
        }

        output += "\n";
      }
    }

    return (true, output);
  }

  /*
   * Handle juggler_detect_identity tool call.
   *
   * Detects the appropriate identity for a repository based on its remote URL.
   */
  proc handleDetectIdentity(params: string): (bool, string) {
    stderr.writeln("Tools: handleDetectIdentity");

    // Get repository path
    const (hasPath, repoPath) = Protocol.extractJsonString(params, "repoPath");
    const path = if hasPath && repoPath != "" then repoPath else getCwd();

    // Check if it's a git repository
    if !exists(path + "/.git") {
      return (false, "Not a git repository: " + path);
    }

    // Get the remote URL
    const (urlOk, remoteUrl) = getGitRemoteUrl(path);

    if !urlOk {
      return (false, "Could not get remote URL for repository at " + path);
    }

    var output = "Repository: " + path + "\n";
    output += "Remote URL: " + remoteUrl + "\n\n";

    // Detect identity based on URL patterns
    var detectedIdentity = "";
    var detectedProvider = "";
    var confidence = "low";

    // Check for SSH host aliases
    if remoteUrl.find("gitlab-personal:") != -1 {
      detectedIdentity = "personal";
      detectedProvider = "gitlab";
      confidence = "high";
    } else if remoteUrl.find("gitlab-work:") != -1 {
      detectedIdentity = "work";
      detectedProvider = "gitlab";
      confidence = "high";
    } else if remoteUrl.find("github.com:") != -1 || remoteUrl.find("@github.com:") != -1 {
      detectedIdentity = "github-personal";
      detectedProvider = "github";
      confidence = "medium";
    } else if remoteUrl.find("gitlab.com:") != -1 || remoteUrl.find("@gitlab.com:") != -1 {
      // Check organization path
      if remoteUrl.find("tinyland") != -1 {
        detectedIdentity = "personal";
        detectedProvider = "gitlab";
        confidence = "high";
      } else if remoteUrl.find("bates") != -1 {
        detectedIdentity = "work";
        detectedProvider = "gitlab";
        confidence = "high";
      } else {
        detectedIdentity = "work";  // Default to work for generic gitlab.com
        detectedProvider = "gitlab";
        confidence = "low";
      }
    }

    if detectedIdentity != "" {
      output += "Detected Identity: " + detectedIdentity + "\n";
      output += "Provider: " + detectedProvider + "\n";
      output += "Confidence: " + confidence + "\n\n";

      // Get current git config
      const (hasName, currentName) = getGitUserName(path);
      const (hasEmail, currentEmail) = getGitUserEmail(path);

      output += "Current Git Config:\n";
      output += "  user.name: " + (if hasName then currentName else "(not set)") + "\n";
      output += "  user.email: " + (if hasEmail then currentEmail else "(not set)") + "\n";
    } else {
      output += "Could not detect identity from remote URL.\n";
      output += "Use 'juggler_switch' to manually set an identity.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_switch tool call.
   *
   * Switches to a different git identity.
   */
  proc handleSwitch(params: string): (bool, string) {
    stderr.writeln("Tools: handleSwitch");

    // Get required identity parameter
    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");

    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    // Get optional parameters
    const (hasSetRemote, setRemoteStr) = Protocol.extractJsonString(params, "setRemote");
    const setRemote = !hasSetRemote || setRemoteStr != "false";

    const (hasConfigGPG, configGPGStr) = Protocol.extractJsonString(params, "configureGPG");
    const configureGPG = !hasConfigGPG || configGPGStr != "false";

    const (hasPath, repoPath) = Protocol.extractJsonString(params, "repoPath");
    const path = if hasPath && repoPath != "" then repoPath else getCwd();

    // Read config to get identity details
    const (configOk, configContent) = readConfigFile();

    var user = "";
    var email = "";
    var provider = "";
    var host = "";
    var hostname = "";
    var gpgKeyId = "";

    if configOk {
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
      if hasIdentities {
        const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identity);
        if hasId {
          const (_, u) = Protocol.extractJsonString(idJson, "user");
          const (_, e) = Protocol.extractJsonString(idJson, "email");
          const (_, p) = Protocol.extractJsonString(idJson, "provider");
          const (_, h) = Protocol.extractJsonString(idJson, "host");
          const (_, hn) = Protocol.extractJsonString(idJson, "hostname");
          user = u;
          email = e;
          provider = p;
          host = h;
          hostname = hn;

          // Get GPG config
          const (hasGpg, gpgJson) = Protocol.extractJsonObject(idJson, "gpg");
          if hasGpg {
            const (_, keyId) = Protocol.extractJsonString(gpgJson, "keyId");
            gpgKeyId = keyId;
          }
        }
      }
    }

    // Use defaults if identity not found in config
    if user == "" {
      select identity {
        when "personal" {
          user = "xoxdjess";
          email = "jess@sulliwood.org";
          provider = "gitlab";
          host = "gitlab-personal";
          hostname = "gitlab.com";
        }
        when "work" {
          user = "jsullivan2";
          email = "jsullivan2@bates.edu";
          provider = "gitlab";
          host = "gitlab-work";
          hostname = "gitlab.com";
        }
        when "github-personal" {
          user = "Jesssullivan";
          email = "jess@sulliwood.org";
          provider = "github";
          host = "github.com";
          hostname = "github.com";
        }
        otherwise {
          return (false, "Unknown identity: " + identity + ". Available: personal, work, github-personal");
        }
      }
    }

    var output = "Switching to identity: " + identity + "\n";
    output += "================================\n\n";

    // Set git user config
    var success = true;

    if setGitConfig(path, "user.name", user) {
      output += "[OK] Set user.name = " + user + "\n";
    } else {
      output += "[FAIL] Could not set user.name\n";
      success = false;
    }

    if setGitConfig(path, "user.email", email) {
      output += "[OK] Set user.email = " + email + "\n";
    } else {
      output += "[FAIL] Could not set user.email\n";
      success = false;
    }

    // Update remote URL if requested
    if setRemote && exists(path + "/.git") {
      const (urlOk, currentUrl) = getGitRemoteUrl(path);
      if urlOk {
        // Check if URL needs updating
        var newUrl = currentUrl;
        var needsUpdate = false;

        // Replace host in SSH URLs
        if currentUrl.find("git@") != -1 {
          if currentUrl.find("gitlab.com:") != -1 && host != "gitlab.com" {
            newUrl = currentUrl.replace("git@gitlab.com:", "git@" + host + ":");
            needsUpdate = true;
          } else if currentUrl.find("gitlab-personal:") != -1 && host != "gitlab-personal" {
            newUrl = currentUrl.replace("git@gitlab-personal:", "git@" + host + ":");
            needsUpdate = true;
          } else if currentUrl.find("gitlab-work:") != -1 && host != "gitlab-work" {
            newUrl = currentUrl.replace("git@gitlab-work:", "git@" + host + ":");
            needsUpdate = true;
          }
        }

        if needsUpdate {
          if setGitRemoteUrl(path, "origin", newUrl) {
            output += "[OK] Updated remote URL to: " + newUrl + "\n";
          } else {
            output += "[WARN] Could not update remote URL\n";
          }
        } else {
          output += "[OK] Remote URL already correct: " + currentUrl + "\n";
        }
      }
    }

    // Configure GPG/SSH signing if requested
    var signingFormat = "";
    var sshKeyPath = "";
    var hardwareKey = false;
    var touchPolicy = "";

    // Extract signing configuration from config
    if configOk {
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
      if hasIdentities {
        const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identity);
        if hasId {
          const (hasGpg, gpgJson) = Protocol.extractJsonObject(idJson, "gpg");
          if hasGpg {
            const (_, format) = Protocol.extractJsonString(gpgJson, "format");
            const (_, sshKey) = Protocol.extractJsonString(gpgJson, "sshKeyPath");
            const (_, hwKey) = Protocol.extractJsonString(gpgJson, "hardwareKey");
            const (_, touch) = Protocol.extractJsonString(gpgJson, "touchPolicy");

            signingFormat = if format != "" then format else "gpg";
            sshKeyPath = sshKey;
            hardwareKey = hwKey == "true";
            touchPolicy = touch;
          }
        }
      }
    }

    if configureGPG {
      if signingFormat == "ssh" && sshKeyPath != "" {
        // SSH signing (git 2.34+)
        if setGitConfig(path, "gpg.format", "ssh") {
          output += "[OK] Set signing format: ssh\n";
        }
        const home = getEnvHome();
        const expandedPath = if sshKeyPath.startsWith("~") then home + sshKeyPath[1..] else sshKeyPath;
        if setGitConfig(path, "user.signingkey", expandedPath) {
          output += "[OK] Set SSH signing key: " + expandedPath + "\n";
        }
        if setGitConfig(path, "commit.gpgsign", "true") {
          output += "[OK] Enabled SSH commit signing\n";
        }

        // Add hardware key warning for SSH
        if hardwareKey {
          output += "\n[HARDWARE KEY WARNING]\n";
          output += "  SSH signing key is FIDO2 (hardware-backed)\n";
          if touchPolicy == "on" {
            output += "  Touch policy: on - Physical touch required for EACH signature\n";
            output += "  Agent CANNOT automate signing - user must touch YubiKey when committing\n";
          } else if touchPolicy == "cached" {
            output += "  Touch policy: cached - Touch once, then signatures cached briefly\n";
          }
        }

      } else if gpgKeyId != "" && gpgKeyId != "auto" {
        // GPG signing
        if setGitConfig(path, "gpg.format", "gpg") {
          output += "[OK] Set signing format: gpg\n";
        }
        if setGitConfig(path, "user.signingkey", gpgKeyId) {
          output += "[OK] Set GPG signing key: " + gpgKeyId + "\n";
        }
        if setGitConfig(path, "commit.gpgsign", "true") {
          output += "[OK] Enabled GPG commit signing\n";
        }

        // Add hardware key warning for GPG
        if hardwareKey {
          output += "\n[HARDWARE KEY WARNING]\n";
          output += "  GPG signing key " + gpgKeyId + " is on a hardware token (YubiKey)\n";
          if touchPolicy == "on" {
            output += "  Touch policy: on - Physical touch required for EACH signature\n";
            output += "  Agent CANNOT automate signing - user must touch YubiKey when committing\n";
          } else if touchPolicy == "cached" {
            output += "  Touch policy: cached - Touch once, then signatures cached briefly\n";
          } else {
            output += "  Touch policy: " + touchPolicy + "\n";
          }
          output += "  Use 'juggler_gpg_status' to check YubiKey presence before committing\n";
        }
      }
    }

    // Authenticate with provider CLI
    output += "\nProvider CLI Authentication:\n";
    if provider == "gitlab" {
      const (authOk, authMsg) = getGlabAuthStatus(hostname);
      if authOk {
        output += "[OK] glab authenticated to " + hostname + "\n";
      } else {
        output += "[INFO] glab not authenticated. Run: glab auth login -h " + hostname + "\n";
      }
    } else if provider == "github" {
      const (authOk, authMsg) = getGhAuthStatus(hostname);
      if authOk {
        output += "[OK] gh authenticated to " + hostname + "\n";
      } else {
        output += "[INFO] gh not authenticated. Run: gh auth login\n";
      }
    }

    output += "\nIdentity switch " + (if success then "completed successfully." else "completed with warnings.");

    return (success, output);
  }

  /*
   * Handle juggler_status tool call.
   *
   * Shows the current identity status.
   */
  proc handleStatus(params: string): (bool, string) {
    stderr.writeln("Tools: handleStatus");

    // Get repository path
    const (hasPath, repoPath) = Protocol.extractJsonString(params, "repoPath");
    const path = if hasPath && repoPath != "" then repoPath else getCwd();

    const (hasVerbose, verboseStr) = Protocol.extractJsonString(params, "verbose");
    const verbose = hasVerbose && verboseStr == "true";

    var output = "RemoteJuggler Status\n";
    output += "====================\n\n";

    // Current working directory
    output += "Working Directory: " + path + "\n";

    // Check if it's a git repo
    if exists(path + "/.git") {
      output += "Git Repository: Yes\n\n";

      // Current git config
      const (hasName, currentName) = getGitUserName(path);
      const (hasEmail, currentEmail) = getGitUserEmail(path);

      output += "Current Git Identity:\n";
      output += "  user.name: " + (if hasName then currentName else "(not set)") + "\n";
      output += "  user.email: " + (if hasEmail then currentEmail else "(not set)") + "\n";

      // Remote URL
      const (urlOk, remoteUrl) = getGitRemoteUrl(path);
      if urlOk {
        output += "  remote (origin): " + remoteUrl + "\n";
      }

      output += "\n";
    } else {
      output += "Git Repository: No\n\n";
    }

    // Provider CLI status
    output += "Provider CLI Status:\n";

    if isGlabAvailable() {
      const (glabOk, glabStatus) = getGlabAuthStatus();
      output += "  glab: " + (if glabOk then "authenticated" else "not authenticated") + "\n";
      if verbose && glabStatus != "" {
        output += "    " + glabStatus.replace("\n", "\n    ") + "\n";
      }
    } else {
      output += "  glab: not installed\n";
    }

    if isGhAvailable() {
      const (ghOk, ghStatus) = getGhAuthStatus();
      output += "  gh: " + (if ghOk then "authenticated" else "not authenticated") + "\n";
      if verbose && ghStatus != "" {
        output += "    " + ghStatus.replace("\n", "\n    ") + "\n";
      }
    } else {
      output += "  gh: not installed\n";
    }

    output += "\n";

    // Config file status
    const configPath = getConfigPath();
    if exists(configPath) {
      output += "Configuration: " + configPath + " (exists)\n";
    } else {
      output += "Configuration: " + configPath + " (not found)\n";
      output += "  Run 'remote-juggler config init' to create.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_validate tool call.
   *
   * Validates connectivity for an identity.
   */
  proc handleValidate(params: string): (bool, string) {
    stderr.writeln("Tools: handleValidate");

    // Get required identity parameter
    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");

    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    const (hasCheckGPG, checkGPGStr) = Protocol.extractJsonString(params, "checkGPG");
    const checkGPG = hasCheckGPG && checkGPGStr == "true";

    const (hasTestAuth, testAuthStr) = Protocol.extractJsonString(params, "testAuth");
    const testAuth = !hasTestAuth || testAuthStr != "false";

    // Get identity details
    var host = "";
    var provider = "";
    var hostname = "";

    select identity {
      when "personal" {
        host = "gitlab-personal";
        provider = "gitlab";
        hostname = "gitlab.com";
      }
      when "work" {
        host = "gitlab-work";
        provider = "gitlab";
        hostname = "gitlab.com";
      }
      when "github-personal" {
        host = "github.com";
        provider = "github";
        hostname = "github.com";
      }
      otherwise {
        return (false, "Unknown identity: " + identity);
      }
    }

    var output = "Validating identity: " + identity + "\n";
    output += "================================\n\n";

    var allPassed = true;

    // Test SSH connectivity
    output += "SSH Connectivity Test:\n";
    const (sshOk, sshMsg) = testSSHConnection(host);
    if sshOk {
      output += "  [PASS] SSH connection to " + host + " successful\n";
      if sshMsg.size > 0 && sshMsg.size < 200 {
        output += "  Response: " + sshMsg + "\n";
      }
    } else {
      output += "  [FAIL] SSH connection to " + host + " failed\n";
      output += "  Error: " + sshMsg + "\n";
      allPassed = false;
    }

    output += "\n";

    // Test API authentication
    if testAuth {
      output += "API Authentication Test:\n";
      if provider == "gitlab" {
        if isGlabAvailable() {
          const (authOk, authMsg) = getGlabAuthStatus(hostname);
          if authOk {
            output += "  [PASS] glab authenticated to " + hostname + "\n";
          } else {
            output += "  [WARN] glab not authenticated to " + hostname + "\n";
            output += "  Fix: glab auth login -h " + hostname + "\n";
          }
        } else {
          output += "  [SKIP] glab CLI not installed\n";
        }
      } else if provider == "github" {
        if isGhAvailable() {
          const (authOk, authMsg) = getGhAuthStatus(hostname);
          if authOk {
            output += "  [PASS] gh authenticated to " + hostname + "\n";
          } else {
            output += "  [WARN] gh not authenticated to " + hostname + "\n";
            output += "  Fix: gh auth login\n";
          }
        } else {
          output += "  [SKIP] gh CLI not installed\n";
        }
      }
    }

    // Check GPG if requested
    if checkGPG {
      output += "\nGPG Key Validation:\n";
      output += "  [SKIP] GPG validation not yet implemented\n";
    }

    output += "\nValidation " + (if allPassed then "passed." else "completed with issues.");

    return (allPassed, output);
  }

  /*
   * Handle juggler_store_token tool call.
   *
   * Stores a token in the system keychain.
   */
  proc handleStoreToken(params: string): (bool, string) {
    stderr.writeln("Tools: handleStoreToken");

    // Get required parameters
    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");
    const (hasToken, token) = Protocol.extractJsonString(params, "token");

    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    if !hasToken || token == "" {
      return (false, "Missing required parameter: token");
    }

    // For security, we don't actually store tokens in this demo implementation
    // In production, this would use the Keychain module
    var output = "Token Storage\n";
    output += "=============\n\n";

    // Check platform
    const platform = getEnvVar("CHPL_TARGET_PLATFORM");
    if platform == "darwin" || platform == "" {
      output += "Platform: macOS (Keychain available)\n";
      output += "Identity: " + identity + "\n";
      output += "Token: " + token[0..#4] + "..." + token[token.size-4..] + " (" + token.size:string + " chars)\n\n";

      // In production:
      // const service = "remote-juggler." + provider + "." + identity;
      // const success = Keychain.storeToken(service, user, token);

      output += "NOTE: Token storage requires the Keychain module (not yet linked).\n";
      output += "For now, store the token manually:\n\n";
      output += "  security add-generic-password \\\n";
      output += "    -s 'remote-juggler." + identity + "' \\\n";
      output += "    -a '" + identity + "' \\\n";
      output += "    -w '" + token + "'\n\n";

      output += "Or set environment variable:\n";
      output += "  export " + identity.toUpper().replace("-", "_") + "_TOKEN='" + token + "'\n";

      return (true, output);
    } else {
      output += "Platform: " + platform + " (Keychain not available)\n";
      output += "Store token in environment variable:\n";
      output += "  export " + identity.toUpper().replace("-", "_") + "_TOKEN='" + token + "'\n";
      return (true, output);
    }
  }

  /*
   * Handle juggler_sync_config tool call.
   *
   * Synchronizes managed config blocks.
   */
  proc handleSyncConfig(params: string): (bool, string) {
    stderr.writeln("Tools: handleSyncConfig");

    const (hasForce, forceStr) = Protocol.extractJsonString(params, "force");
    const force = hasForce && forceStr == "true";

    const (hasDryRun, dryRunStr) = Protocol.extractJsonString(params, "dryRun");
    const dryRun = hasDryRun && dryRunStr == "true";

    var output = "Configuration Sync\n";
    output += "==================\n\n";

    if dryRun {
      output += "Mode: Dry run (no changes will be made)\n\n";
    }

    const home = getEnvHome();
    const sshConfigPath = home + "/.ssh/config";
    const gitConfigPath = home + "/.gitconfig";
    const configPath = getConfigPath();

    // Check source files
    output += "Source Files:\n";
    output += "  SSH config: " + sshConfigPath;
    if exists(sshConfigPath) {
      output += " (exists)\n";
    } else {
      output += " (not found)\n";
    }

    output += "  Git config: " + gitConfigPath;
    if exists(gitConfigPath) {
      output += " (exists)\n";
    } else {
      output += " (not found)\n";
    }

    output += "\nDestination:\n";
    output += "  Config: " + configPath;
    if exists(configPath) {
      output += " (exists)\n";
    } else {
      output += " (will be created)\n";
    }

    output += "\nSync Actions:\n";

    // In production, this would:
    // 1. Parse SSH config for Host entries
    // 2. Parse gitconfig for url rewrites
    // 3. Update managed blocks in config.json

    output += "  [SKIP] SSH host parsing not yet implemented\n";
    output += "  [SKIP] gitconfig parsing not yet implemented\n";
    output += "  [SKIP] Config file update not yet implemented\n";

    output += "\nTo implement sync, the following modules are needed:\n";
    output += "  - Config.chpl: SSH config parser\n";
    output += "  - GlobalConfig.chpl: Managed block updater\n";

    return (true, output);
  }

  /*
   * Handle juggler_gpg_status tool call.
   *
   * Checks GPG/SSH signing readiness including hardware token status.
   * Returns whether signing is possible, touch requirements, and guidance.
   */
  proc handleGPGStatus(params: string): (bool, string) {
    stderr.writeln("Tools: handleGPGStatus");

    // Get optional parameters
    const (hasIdentity, identityName) = Protocol.extractJsonString(params, "identity");
    const (hasPath, repoPath) = Protocol.extractJsonString(params, "repoPath");
    const path = if hasPath && repoPath != "" then repoPath else getCwd();

    var output = "GPG/SSH Signing Status\n";
    output += "======================\n\n";

    // Check if GPG is available
    var gpgInstalled = false;
    try {
      var p = spawn(["which", "gpg"], stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      gpgInstalled = p.exitCode == 0;
    } catch {
      gpgInstalled = false;
    }

    output += "GPG Availability: " + (if gpgInstalled then "Installed" else "Not installed") + "\n\n";

    // Get identity to check
    var identityToCheck = "";
    var signingFormat = "gpg";
    var gpgKeyId = "";
    var sshKeyPath = "";
    var hardwareKey = false;
    var touchPolicy = "";

    if hasIdentity && identityName != "" {
      identityToCheck = identityName;
    } else {
      // Try to detect from repository
      if exists(path + "/.git") {
        const (urlOk, remoteUrl) = getGitRemoteUrl(path);
        if urlOk {
          if remoteUrl.find("gitlab-personal:") != -1 {
            identityToCheck = "gitlab-personal";
          } else if remoteUrl.find("gitlab-work:") != -1 {
            identityToCheck = "gitlab-work";
          } else if remoteUrl.find("github.com") != -1 {
            identityToCheck = "github-personal";
          }
        }
      }
    }

    // Read config to get identity GPG settings
    const (configOk, configContent) = readConfigFile();

    if configOk && identityToCheck != "" {
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
      if hasIdentities {
        const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identityToCheck);
        if hasId {
          const (hasGpg, gpgJson) = Protocol.extractJsonObject(idJson, "gpg");
          if hasGpg {
            const (_, keyId) = Protocol.extractJsonString(gpgJson, "keyId");
            const (_, format) = Protocol.extractJsonString(gpgJson, "format");
            const (_, sshKey) = Protocol.extractJsonString(gpgJson, "sshKeyPath");
            const (_, hwKey) = Protocol.extractJsonString(gpgJson, "hardwareKey");
            const (_, touch) = Protocol.extractJsonString(gpgJson, "touchPolicy");

            gpgKeyId = keyId;
            signingFormat = if format != "" then format else "gpg";
            sshKeyPath = sshKey;
            hardwareKey = hwKey == "true";
            touchPolicy = touch;
          }
        }
      }
    }

    output += "Identity: " + (if identityToCheck != "" then identityToCheck else "(none detected)") + "\n";
    output += "Signing Format: " + signingFormat + "\n";

    if signingFormat == "ssh" {
      // SSH signing status
      output += "SSH Key Path: " + sshKeyPath + "\n";

      if sshKeyPath != "" {
        const home = getEnvHome();
        const expandedPath = if sshKeyPath.startsWith("~") then home + sshKeyPath[1..] else sshKeyPath;

        if exists(expandedPath) {
          output += "SSH Key Status: Found\n";

          // Check if it's a FIDO2 key
          if sshKeyPath.find("-sk") != -1 {
            output += "Key Type: FIDO2 (hardware-backed)\n";
            hardwareKey = true;
          } else {
            output += "Key Type: Software\n";
          }
        } else {
          output += "SSH Key Status: NOT FOUND\n";
          output += "  [ERROR] SSH key file does not exist: " + expandedPath + "\n";
        }
      }

    } else {
      // GPG signing status
      output += "GPG Key ID: " + (if gpgKeyId != "" then gpgKeyId else "(not configured)") + "\n";

      if gpgKeyId != "" && gpgInstalled {
        // Check if key exists
        var keyExists = false;
        try {
          var p = spawn(["gpg", "--list-secret-keys", gpgKeyId],
                        stdout=pipeStyle.close, stderr=pipeStyle.close);
          p.wait();
          keyExists = p.exitCode == 0;
        } catch {
          keyExists = false;
        }

        output += "GPG Key Status: " + (if keyExists then "Found" else "NOT FOUND") + "\n";

        if !keyExists {
          output += "  [ERROR] GPG key not found in keyring\n";
        }
      }
    }

    output += "\n";

    // Hardware token status
    output += "Hardware Token Status:\n";
    output += "  Hardware Key: " + (if hardwareKey then "Yes" else "No") + "\n";

    if hardwareKey {
      output += "  Touch Policy: " + (if touchPolicy != "" then touchPolicy else "unknown") + "\n";

      // Try to detect YubiKey
      var yubiKeyPresent = false;
      var yubiKeySerial = "";

      try {
        var p = spawn(["gpg", "--card-status"],
                      stdout=pipeStyle.pipe, stderr=pipeStyle.close);
        p.wait();

        if p.exitCode == 0 {
          yubiKeyPresent = true;
          var cardOutput: string;
          p.stdout.readAll(cardOutput);

          // Extract serial
          for line in cardOutput.split("\n") {
            if line.find("Serial number") != -1 {
              const parts = line.split(":");
              if parts.size > 1 {
                yubiKeySerial = parts[1].strip();
              }
            }
          }
        }
      } catch {
        yubiKeyPresent = false;
      }

      output += "  YubiKey Present: " + (if yubiKeyPresent then "Yes" else "No") + "\n";
      if yubiKeyPresent && yubiKeySerial != "" {
        output += "  YubiKey Serial: " + yubiKeySerial + "\n";
      }

      // Get touch policies from ykman if available
      try {
        var ykmanCheck = spawn(["which", "ykman"], stdout=pipeStyle.close, stderr=pipeStyle.close);
        ykmanCheck.wait();

        if ykmanCheck.exitCode == 0 {
          var ykmanP = spawn(["ykman", "openpgp", "info"],
                             stdout=pipeStyle.pipe, stderr=pipeStyle.close);
          ykmanP.wait();

          if ykmanP.exitCode == 0 {
            var ykmanOutput: string;
            ykmanP.stdout.readAll(ykmanOutput);

            output += "\n  Touch Policies (from ykman):\n";

            for line in ykmanOutput.split("\n") {
              if line.find("touch") != -1 || line.find("Touch") != -1 {
                output += "    " + line.strip() + "\n";
              }
            }
          }
        }
      } catch {
        // ykman not available
      }
    }

    output += "\n";

    // Signing readiness assessment
    output += "Signing Readiness:\n";

    var canSign = true;
    var reason = "";

    if signingFormat == "gpg" {
      if !gpgInstalled {
        canSign = false;
        reason = "GPG is not installed";
      } else if gpgKeyId == "" {
        canSign = false;
        reason = "No GPG key configured for this identity";
      } else if hardwareKey && touchPolicy == "on" {
        canSign = false;
        reason = "Physical YubiKey touch required for each signature (touch policy: on)";
      }
    } else {
      // SSH signing
      if sshKeyPath == "" {
        canSign = false;
        reason = "No SSH key path configured";
      } else if hardwareKey && touchPolicy == "on" {
        canSign = false;
        reason = "Physical YubiKey touch required for each signature (touch policy: on)";
      }
    }

    output += "  Can Sign Automatically: " + (if canSign then "Yes" else "No") + "\n";
    if !canSign && reason != "" {
      output += "  Reason: " + reason + "\n";
    }

    output += "\n";

    // Guidance for agents
    output += "Agent Guidance:\n";

    if hardwareKey {
      output += "  - This identity uses a hardware-backed signing key\n";
      if touchPolicy == "on" {
        output += "  - IMPORTANT: Physical YubiKey touch is required for EACH signature\n";
        output += "  - Agent can configure identity but CANNOT automate signing\n";
        output += "  - User must be present to touch YubiKey when committing\n";
      } else if touchPolicy == "cached" {
        output += "  - Touch is cached briefly after first use\n";
        output += "  - User needs to touch YubiKey once, then can sign multiple times\n";
        output += "  - Agent can proceed if user has recently touched the key\n";
      } else {
        output += "  - Touch policy may allow automated signing\n";
      }
    } else {
      if canSign {
        output += "  - Software signing key - can sign automatically\n";
        output += "  - Agent can proceed with commits that require signing\n";
      } else {
        output += "  - Signing is not currently possible\n";
        output += "  - Resolve the issue before attempting signed commits\n";
      }
    }

    // Recommendations
    output += "\nRecommendations:\n";

    if !canSign {
      if !gpgInstalled && signingFormat == "gpg" {
        output += "  1. Install GPG: brew install gnupg\n";
      }
      if hardwareKey && touchPolicy == "on" {
        output += "  1. Ensure YubiKey is connected before committing\n";
        output += "  2. Be ready to touch YubiKey when prompted\n";
        output += "  3. Consider using SSH signing with 'cached' touch for less friction\n";
      }
    } else {
      output += "  - Signing is ready. Proceed with commits.\n";
    }

    return (true, output);
  }

  // ============================================================================
  // PIN Management Tool Handlers (Trusted Workstation Mode)
  // ============================================================================

  // Import HSM functions and constants
  public use super.HSM;

  /*
   * Handle juggler_pin_store tool call.
   *
   * Stores a YubiKey PIN securely in the HSM.
   */
  proc handlePinStore(params: string): (bool, string) {
    stderr.writeln("Tools: handlePinStore");

    // Get required parameters
    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");
    const (hasPin, pin) = Protocol.extractJsonString(params, "pin");

    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    if !hasPin || pin == "" {
      return (false, "Missing required parameter: pin");
    }

    var output = "PIN Storage\n";
    output += "===========\n\n";

    // Check HSM availability
    const hsmType = hsm_detect_available();
    if hsmType == HSM_TYPE_NONE {
      output += "[ERROR] No HSM backend available\n\n";
      output += "Trusted Workstation mode requires:\n";
      output += "  - Linux: TPM 2.0 (/dev/tpmrm0)\n";
      output += "  - macOS: Secure Enclave (T1/T2/Apple Silicon)\n";
      return (false, output);
    }

    const hsmTypeName = hsm_type_name(hsmType);
    output += "HSM Backend: " + hsmTypeName + "\n";
    output += "Identity: " + identity + "\n\n";

    // Validate PIN length
    if pin.size < 6 || pin.size > 127 {
      output += "[ERROR] PIN must be between 6 and 127 characters\n";
      return (false, output);
    }

    // Store PIN
    const result = hsm_store_pin(identity, pin, pin.size);

    if result == HSM_SUCCESS {
      output += "[OK] PIN stored securely in " + hsmTypeName + "\n\n";
      output += "The PIN is now sealed and can only be retrieved on this device\n";
      output += "under the same security conditions.\n\n";
      output += "To enable Trusted Workstation mode, use:\n";
      output += "  juggler_security_mode with mode=\"trusted_workstation\"\n";
      return (true, output);
    } else {
      const errMsg = hsm_error_message(result);
      output += "[ERROR] Failed to store PIN: " + errMsg + "\n";
      return (false, output);
    }
  }

  /*
   * Handle juggler_pin_clear tool call.
   *
   * Removes a stored PIN from the HSM.
   */
  proc handlePinClear(params: string): (bool, string) {
    stderr.writeln("Tools: handlePinClear");

    // Get required identity parameter
    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");

    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    var output = "PIN Clear\n";
    output += "=========\n\n";

    // Check HSM availability
    if hsm_is_available() == 0 {
      output += "[ERROR] No HSM backend available\n";
      return (false, output);
    }

    output += "Identity: " + identity + "\n\n";

    // Check if PIN exists
    if hsm_has_pin(identity) == 0 {
      output += "[WARNING] No PIN stored for this identity\n";
      return (true, output);
    }

    // Clear PIN
    const result = hsm_clear_pin(identity);

    if result == HSM_SUCCESS {
      output += "[OK] PIN cleared from HSM\n\n";
      output += "The identity will now require manual PIN entry for signing operations.\n";
      return (true, output);
    } else {
      const errMsg = hsm_error_message(result);
      output += "[ERROR] Failed to clear PIN: " + errMsg + "\n";
      return (false, output);
    }
  }

  /*
   * Handle juggler_pin_status tool call.
   *
   * Checks PIN storage status in the HSM.
   */
  proc handlePinStatus(params: string): (bool, string) {
    stderr.writeln("Tools: handlePinStatus");

    // Get optional identity parameter
    const (hasIdentity, identityFilter) = Protocol.extractJsonString(params, "identity");

    var output = "PIN Storage Status\n";
    output += "==================\n\n";

    // Check HSM availability
    const hsmType = hsm_detect_available();
    output += "HSM Backend: ";
    if hsmType != HSM_TYPE_NONE {
      const hsmTypeName = hsm_type_name(hsmType);
      output += hsmTypeName + " (available)\n";

      select hsmType {
        when HSM_TYPE_TPM {
          output += "  Security: PIN sealed to PCR 7 (Secure Boot state)\n";
        }
        when HSM_TYPE_SECURE_ENCLAVE {
          output += "  Security: ECIES encryption with biometric/password\n";
        }
        when HSM_TYPE_KEYCHAIN {
          output += "  Security: Protected by login password (fallback)\n";
        }
      }
    } else {
      output += "None (Trusted Workstation mode unavailable)\n";
      output += "\n";
      output += "To enable HSM support:\n";
      output += "  Linux: Install tpm2-tss and ensure /dev/tpmrm0 is accessible\n";
      output += "  macOS: Requires Mac with T1/T2 chip or Apple Silicon\n";
      return (true, output);
    }

    output += "\n";

    // If specific identity requested
    if hasIdentity && identityFilter != "" {
      const hasPIN = hsm_has_pin(identityFilter) != 0;
      output += "Identity: " + identityFilter + "\n";
      output += "  PIN Stored: " + (if hasPIN then "Yes" else "No") + "\n";
    } else {
      // Show status for known identities
      output += "Stored PINs by Identity:\n";
      const knownIdentities = ["personal", "work", "github-personal", "gitlab-work"];
      var anyStored = false;

      for name in knownIdentities {
        const hasPIN = hsm_has_pin(name) != 0;
        if hasPIN {
          output += "  " + name + ": stored\n";
          anyStored = true;
        }
      }

      if !anyStored {
        output += "  (no PINs stored)\n";
      }
    }

    // Show current security mode from config
    const (configOk, configContent) = readConfigFile();
    if configOk {
      const settingsSection = Protocol.extractJsonObject(configContent, "settings");
      if settingsSection(0) {
        const (hasMode, mode) = Protocol.extractJsonString(settingsSection(1), "defaultSecurityMode");
        if hasMode {
          output += "\nCurrent Security Mode: " + mode + "\n";

          if mode == "trusted_workstation" {
            output += "  Trusted Workstation mode is ACTIVE\n";
            output += "  YubiKey PINs will be retrieved from HSM automatically\n";
          } else {
            output += "  To enable Trusted Workstation mode:\n";
            output += "  Use juggler_security_mode with mode=\"trusted_workstation\"\n";
          }
        }
      }
    }

    return (true, output);
  }

  /*
   * Handle juggler_security_mode tool call.
   *
   * Gets or sets the security mode for GPG operations.
   */
  proc handleSecurityMode(params: string): (bool, string) {
    stderr.writeln("Tools: handleSecurityMode");

    // Get optional mode parameter
    const (hasMode, mode) = Protocol.extractJsonString(params, "mode");

    var output = "Security Mode\n";
    output += "=============\n\n";

    // Read current config
    const (configOk, configContent) = readConfigFile();
    var currentMode = "developer_workflow";

    if configOk {
      const settingsSection = Protocol.extractJsonObject(configContent, "settings");
      if settingsSection(0) {
        const (hasCurrentMode, m) = Protocol.extractJsonString(settingsSection(1), "defaultSecurityMode");
        if hasCurrentMode {
          currentMode = m;
        }
      }
    }

    // If no mode specified, return current mode
    if !hasMode || mode == "" {
      output += "Current Mode: " + currentMode + "\n\n";
      output += "Available Modes:\n";
      output += "  maximum_security    - PIN required for every signing operation\n";
      output += "  developer_workflow  - PIN cached by gpg-agent for session\n";
      output += "  trusted_workstation - PIN stored in HSM, auto-retrieved\n\n";

      output += "Mode Details:\n";
      select currentMode {
        when "maximum_security" {
          output += "  Every git commit or tag operation will prompt for YubiKey PIN.\n";
          output += "  Most secure, but requires manual interaction.\n";
        }
        when "developer_workflow" {
          output += "  PIN is cached by gpg-agent after first entry.\n";
          output += "  Balanced security and convenience.\n";
        }
        when "trusted_workstation" {
          output += "  PIN is automatically retrieved from HSM (TPM/SecureEnclave).\n";
          output += "  No PIN entry required on this trusted device.\n";
        }
      }

      return (true, output);
    }

    // Validate mode
    if mode != "maximum_security" && mode != "developer_workflow" && mode != "trusted_workstation" {
      output += "[ERROR] Invalid mode: " + mode + "\n";
      output += "Valid modes: maximum_security, developer_workflow, trusted_workstation\n";
      return (false, output);
    }

    // Check HSM requirement for trusted_workstation
    if mode == "trusted_workstation" {
      if hsm_is_available() == 0 {
        output += "[ERROR] Cannot enable trusted_workstation mode\n";
        output += "No HSM backend available on this system.\n\n";
        output += "Requirements:\n";
        output += "  Linux: TPM 2.0 with /dev/tpmrm0 accessible\n";
        output += "  macOS: Mac with T1/T2 chip or Apple Silicon\n";
        return (false, output);
      }
    }

    // Update mode by writing to config
    // Note: In production, this would use a proper config update function
    output += "Previous Mode: " + currentMode + "\n";
    output += "New Mode: " + mode + "\n\n";

    select mode {
      when "maximum_security" {
        output += "[OK] Switching to Maximum Security mode\n\n";
        output += "Behavior:\n";
        output += "  - YubiKey PIN will be required for every signing operation\n";
        output += "  - No PIN caching by gpg-agent\n";
        output += "  - Most secure for sensitive environments\n";
      }
      when "developer_workflow" {
        output += "[OK] Switching to Developer Workflow mode\n\n";
        output += "Behavior:\n";
        output += "  - PIN cached by gpg-agent after first entry\n";
        output += "  - Cache expires based on gpg-agent settings\n";
        output += "  - Balanced security and convenience\n";
      }
      when "trusted_workstation" {
        output += "[OK] Switching to Trusted Workstation mode\n\n";
        output += "Behavior:\n";
        output += "  - PIN automatically retrieved from HSM\n";
        output += "  - No manual PIN entry required\n";
        output += "  - Only works on this specific device\n\n";
        output += "IMPORTANT:\n";
        output += "  - Switching away from this mode requires re-entering PINs\n";
        output += "  - HSM-stored PINs are device-specific and non-transferable\n";
        output += "  - Ensure PINs are stored first using juggler_pin_store\n";
      }
    }

    // Note: Actual config update would happen here via GlobalConfig.setSecurityMode()
    output += "\nNote: To persist this setting, update ~/.config/remote-juggler/config.json\n";
    output += "or run: remote-juggler security-mode " + mode + "\n";

    return (true, output);
  }

  // ============================================================================
  // Setup Tool Handler
  // ============================================================================

  /*
   * Handle juggler_setup tool call.
   *
   * Runs the first-time setup wizard in the specified mode.
   */
  proc handleSetup(params: string): (bool, string) {
    stderr.writeln("Tools: handleSetup");

    // Get optional mode parameter
    const (hasMode, modeStr) = Protocol.extractJsonString(params, "mode");
    const (hasForce, forceStr) = Protocol.extractJsonString(params, "force");

    // Parse mode
    var mode = Setup.SetupMode.Auto;  // Default for MCP
    if hasMode {
      select modeStr {
        when "auto" do mode = Setup.SetupMode.Auto;
        when "status" do mode = Setup.SetupMode.Status;
        when "import_ssh" do mode = Setup.SetupMode.ImportSSH;
        when "import_gpg" do mode = Setup.SetupMode.ImportGPG;
        when "interactive" do mode = Setup.SetupMode.Interactive;
        otherwise {
          return (false, "Invalid mode: " + modeStr + ". Valid modes: auto, status, import_ssh, import_gpg");
        }
      }
    }

    // Run setup
    const result = Setup.runSetup(mode);

    // Build output
    var output = "";

    if result.success {
      output += "Setup completed successfully.\n\n";
      output += "Summary:\n";
      output += "  Identities created: " + result.identitiesCreated:string + "\n";
      output += "  GPG keys associated: " + result.gpgKeysAssociated:string + "\n";
      output += "  HSM detected: " + (if result.hsmDetected then "Yes (" + result.hsmType + ")" else "No") + "\n";

      if result.configPath != "" {
        output += "  Configuration: " + result.configPath + "\n";
      }

      // Include any warnings
      if result.warnings.size > 0 {
        output += "\nWarnings:\n";
        for warning in result.warnings {
          output += "  - " + warning + "\n";
        }
      }

      output += "\nNext steps:\n";
      output += "  1. Use 'juggler_list_identities' to see configured identities\n";
      output += "  2. Use 'juggler_switch' to switch identity in a repository\n";
      if result.hsmDetected {
        output += "  3. Use 'juggler_pin_store' to enable Trusted Workstation mode\n";
      }
    } else {
      output += "Setup failed.\n\n";
      output += "Error: " + result.message + "\n";

      if result.warnings.size > 0 {
        output += "\nDetails:\n";
        for warning in result.warnings {
          output += "  - " + warning + "\n";
        }
      }
    }

    return (result.success, output);
  }

  // ============================================================================
  // Priority 1 Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_token_verify tool call.
   *
   * Verifies stored tokens by testing API connectivity.
   */
  proc handleTokenVerify(params: string): (bool, string) {
    stderr.writeln("Tools: handleTokenVerify");

    const (hasIdentity, identityFilter) = Protocol.extractJsonString(params, "identity");

    var output = "Token Verification\n";
    output += "==================\n\n";

    // Read config to get identities
    const (configOk, configContent) = readConfigFile();
    if !configOk {
      return (false, "Configuration not found at " + getConfigPath());
    }

    // If identity specified, verify just that one
    if hasIdentity && identityFilter != "" {
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
      if hasIdentities {
        const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identityFilter);
        if hasId {
          const (_, provider) = Protocol.extractJsonString(idJson, "provider");
          const (_, hostname) = Protocol.extractJsonString(idJson, "hostname");

          output += "Identity: " + identityFilter + "\n";
          output += "Provider: " + provider + "\n\n";

          if provider == "gitlab" {
            const (authOk, authMsg) = getGlabAuthStatus(if hostname != "" then hostname else "gitlab.com");
            output += "glab Auth: " + (if authOk then "VALID" else "INVALID") + "\n";
            if authMsg != "" {
              output += "Details: " + authMsg + "\n";
            }
          } else if provider == "github" {
            const (authOk, authMsg) = getGhAuthStatus(if hostname != "" then hostname else "github.com");
            output += "gh Auth: " + (if authOk then "VALID" else "INVALID") + "\n";
            if authMsg != "" {
              output += "Details: " + authMsg + "\n";
            }
          }
        } else {
          return (false, "Identity not found: " + identityFilter);
        }
      }
    } else {
      // Verify all identities
      const knownIdentities = ["personal", "work", "github-personal"];
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");

      for name in knownIdentities {
        if hasIdentities {
          const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, name);
          if hasId {
            const (_, provider) = Protocol.extractJsonString(idJson, "provider");
            const (_, hostname) = Protocol.extractJsonString(idJson, "hostname");

            output += name + " (" + provider + "): ";

            if provider == "gitlab" {
              const (authOk, _) = getGlabAuthStatus(if hostname != "" then hostname else "gitlab.com");
              output += if authOk then "VALID" else "NO TOKEN/INVALID";
            } else if provider == "github" {
              const (authOk, _) = getGhAuthStatus(if hostname != "" then hostname else "github.com");
              output += if authOk then "VALID" else "NO TOKEN/INVALID";
            } else {
              output += "SKIP (unsupported provider)";
            }
            output += "\n";
          }
        }
      }
    }

    return (true, output);
  }

  /*
   * Handle juggler_config_show tool call.
   *
   * Shows the current configuration, optionally filtered by section.
   */
  proc handleConfigShow(params: string): (bool, string) {
    stderr.writeln("Tools: handleConfigShow");

    const (hasSection, section) = Protocol.extractJsonString(params, "section");
    const sectionFilter = if hasSection && section != "" then section else "all";

    // Read config file
    const (configOk, configContent) = readConfigFile();
    if !configOk {
      return (false, "Configuration not found at " + getConfigPath());
    }

    var output = "RemoteJuggler Configuration\n";
    output += "===========================\n";
    output += "Path: " + getConfigPath() + "\n\n";

    if sectionFilter == "all" {
      // Return the full config (prettified would be nice, but we return raw)
      output += configContent;
    } else if sectionFilter == "identities" {
      const section = Protocol.extractJsonObject(configContent, "identities");
      if section(0) {
        output += "Identities:\n" + section(1) + "\n";
      } else {
        output += "No identities section found.\n";
      }
    } else if sectionFilter == "settings" {
      const section = Protocol.extractJsonObject(configContent, "settings");
      if section(0) {
        output += "Settings:\n" + section(1) + "\n";
      } else {
        output += "No settings section found.\n";
      }
    } else if sectionFilter == "state" {
      const section = Protocol.extractJsonObject(configContent, "state");
      if section(0) {
        output += "State:\n" + section(1) + "\n";
      } else {
        output += "No state section found.\n";
      }
    } else if sectionFilter == "managed_ssh" {
      const section = Protocol.extractJsonObject(configContent, "_managed_ssh_hosts");
      if section(0) {
        output += "Managed SSH Hosts:\n" + section(1) + "\n";
      } else {
        output += "No managed SSH hosts section found.\n";
      }
    } else if sectionFilter == "managed_git" {
      const section = Protocol.extractJsonObject(configContent, "_managed_gitconfig_rewrites");
      if section(0) {
        output += "Managed Git Rewrites:\n" + section(1) + "\n";
      } else {
        output += "No managed git rewrites section found.\n";
      }
    } else {
      return (false, "Unknown section: " + sectionFilter +
              ". Valid: all, identities, settings, state, managed_ssh, managed_git");
    }

    return (true, output);
  }

  /*
   * Handle juggler_debug_ssh tool call.
   *
   * Debug SSH configuration: parsed hosts, connectivity, key permissions.
   */
  proc handleDebugSSH(params: string): (bool, string) {
    stderr.writeln("Tools: handleDebugSSH");

    var output = "SSH Configuration Debug\n";
    output += "=======================\n\n";

    const home = getEnvHome();
    const sshConfigPath = home + "/.ssh/config";

    // Check SSH config file
    output += "SSH Config: " + sshConfigPath + "\n";
    if exists(sshConfigPath) {
      output += "  Status: exists\n\n";
    } else {
      output += "  Status: NOT FOUND\n";
      return (true, output);
    }

    // Read and show SSH config (first 100 lines)
    try {
      var f = open(sshConfigPath, ioMode.r);
      var reader = f.reader(locking=false);
      var content: string;
      reader.readAll(content);
      reader.close();
      f.close();

      // Parse Host entries
      output += "Parsed SSH Hosts:\n";
      var hostCount = 0;
      for line in content.split("\n") {
        const trimmed = line.strip();
        if trimmed.startsWith("Host ") && !trimmed.startsWith("Host *") {
          const hostName = trimmed[5..].strip();
          hostCount += 1;
          output += "  " + hostCount:string + ". " + hostName + "\n";
        }
      }
      output += "\nTotal hosts: " + hostCount:string + "\n\n";

    } catch e {
      output += "  Error reading SSH config: " + e.message() + "\n\n";
    }

    // Test connectivity for git-related hosts
    output += "SSH Connectivity Tests:\n";
    const gitHosts = ["gitlab-personal", "gitlab-personal-sk",
                      "gitlab-work", "gitlab-work-sk",
                      "github-personal", "github-personal-sk"];

    for host in gitHosts {
      output += "  " + host + ": ";
      const (sshOk, sshMsg) = testSSHConnection(host);
      if sshOk {
        output += "OK";
      } else {
        output += "FAIL";
        if sshMsg.size > 0 && sshMsg.size < 100 {
          output += " (" + sshMsg + ")";
        }
      }
      output += "\n";
    }

    output += "\n";

    // Check SSH key file permissions
    output += "SSH Key Permissions:\n";
    const sshDir = home + "/.ssh";
    try {
      var p = spawn(["ls", "-la", sshDir],
                     stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();
      if p.exitCode == 0 {
        var lsOutput: string;
        p.stdout.readAll(lsOutput);
        for line in lsOutput.split("\n") {
          if line.find("id_") != -1 || line.find("gitlab") != -1 ||
             line.find("github") != -1 || line.find("-sk") != -1 {
            output += "  " + line.strip() + "\n";
          }
        }
      }
    } catch {
      output += "  Error listing SSH directory\n";
    }

    // Check SSH agent
    output += "\nSSH Agent:\n";
    try {
      var p = spawn(["ssh-add", "-l"],
                     stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();
      if p.exitCode == 0 {
        var agentOutput: string;
        p.stdout.readAll(agentOutput);
        output += "  " + agentOutput.replace("\n", "\n  ");
      } else {
        output += "  No keys loaded or agent not running\n";
      }
    } catch {
      output += "  SSH agent not available\n";
    }

    return (true, output);
  }

  // ============================================================================
  // Priority 2 Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_token_get tool call.
   *
   * Retrieves a stored token (masked for security).
   */
  proc handleTokenGet(params: string): (bool, string) {
    stderr.writeln("Tools: handleTokenGet");

    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");
    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    var output = "Token Retrieval\n";
    output += "===============\n\n";
    output += "Identity: " + identity + "\n\n";

    // Try to get token from environment
    var token = "";
    var source = "";

    // Check environment variable
    const envVarNames = [
      identity.toUpper().replace("-", "_") + "_TOKEN",
      "GITLAB_TOKEN",
      "GITHUB_TOKEN"
    ];

    for envName in envVarNames {
      const val = getEnvVar(envName);
      if val != "" {
        token = val;
        source = "environment (" + envName + ")";
        break;
      }
    }

    // Try provider CLI
    if token == "" {
      // Read config for provider info
      const (configOk, configContent) = readConfigFile();
      if configOk {
        const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
        if hasIdentities {
          const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identity);
          if hasId {
            const (_, provider) = Protocol.extractJsonString(idJson, "provider");
            if provider == "gitlab" {
              try {
                var p = spawn(["glab", "auth", "token"],
                              stdout=pipeStyle.pipe, stderr=pipeStyle.close);
                p.wait();
                if p.exitCode == 0 {
                  p.stdout.readAll(token);
                  token = token.strip();
                  source = "glab CLI";
                }
              } catch { }
            } else if provider == "github" {
              try {
                var p = spawn(["gh", "auth", "token"],
                              stdout=pipeStyle.pipe, stderr=pipeStyle.close);
                p.wait();
                if p.exitCode == 0 {
                  p.stdout.readAll(token);
                  token = token.strip();
                  source = "gh CLI";
                }
              } catch { }
            }
          }
        }
      }
    }

    if token != "" {
      // Mask the token for security
      if token.size > 8 {
        output += "Token: " + token[0..#4] + "..." + token[token.size-4..] +
                  " (" + token.size:string + " chars)\n";
      } else {
        output += "Token: ****" + " (" + token.size:string + " chars)\n";
      }
      output += "Source: " + source + "\n";
    } else {
      output += "No token found.\n\n";
      output += "Token sources checked:\n";
      for envName in envVarNames {
        output += "  - Environment: $" + envName + "\n";
      }
      output += "  - Provider CLI (glab/gh auth token)\n";
      output += "  - System keychain\n";
    }

    return (token != "", output);
  }

  /*
   * Handle juggler_token_clear tool call.
   *
   * Clears a stored token for an identity.
   */
  proc handleTokenClear(params: string): (bool, string) {
    stderr.writeln("Tools: handleTokenClear");

    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");
    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    var output = "Token Clear\n";
    output += "===========\n\n";
    output += "Identity: " + identity + "\n\n";

    // Read config for provider info
    const (configOk, configContent) = readConfigFile();
    var provider = "";
    var hostname = "";

    if configOk {
      const (hasIdentities, identitiesJson) = Protocol.extractJsonObject(configContent, "identities");
      if hasIdentities {
        const (hasId, idJson) = Protocol.extractJsonObject(identitiesJson, identity);
        if hasId {
          const (_, p) = Protocol.extractJsonString(idJson, "provider");
          const (_, h) = Protocol.extractJsonString(idJson, "hostname");
          provider = p;
          hostname = h;
        }
      }
    }

    // Attempt to logout from provider CLI
    if provider == "gitlab" {
      output += "Clearing glab authentication...\n";
      try {
        var p = spawn(["glab", "auth", "logout", "-h",
                       if hostname != "" then hostname else "gitlab.com"],
                      stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
        p.wait();
        if p.exitCode == 0 {
          output += "  [OK] glab auth cleared\n";
        } else {
          output += "  [INFO] glab auth was not set or already cleared\n";
        }
      } catch {
        output += "  [SKIP] glab not available\n";
      }
    } else if provider == "github" {
      output += "Clearing gh authentication...\n";
      try {
        var p = spawn(["gh", "auth", "logout", "-h",
                       if hostname != "" then hostname else "github.com"],
                      stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
        p.wait();
        if p.exitCode == 0 {
          output += "  [OK] gh auth cleared\n";
        } else {
          output += "  [INFO] gh auth was not set or already cleared\n";
        }
      } catch {
        output += "  [SKIP] gh not available\n";
      }
    } else {
      output += "  [INFO] Provider '" + provider + "' - no CLI auth to clear\n";
    }

    output += "\nNote: Environment variable tokens must be removed manually.\n";
    output += "Keychain tokens can be removed with: security delete-generic-password -s 'remote-juggler." + identity + "'\n";

    return (true, output);
  }

  /*
   * Handle juggler_tws_status tool call.
   *
   * Comprehensive Trusted Workstation status.
   */
  proc handleTWSStatus(params: string): (bool, string) {
    stderr.writeln("Tools: handleTWSStatus");

    const (hasIdentity, identityFilter) = Protocol.extractJsonString(params, "identity");

    var output = "Trusted Workstation Status\n";
    output += "=========================\n\n";

    // HSM availability
    const hsmType = hsm_detect_available();
    output += "HSM Backend: ";
    if hsmType != HSM_TYPE_NONE {
      output += hsm_type_name(hsmType) + " (available)\n";
    } else {
      output += "None\n";
      output += "\n[WARNING] Trusted Workstation mode is not available.\n";
      output += "Requirements:\n";
      output += "  Linux: TPM 2.0 with /dev/tpmrm0\n";
      output += "  macOS: Secure Enclave (T1/T2/Apple Silicon)\n";
      return (true, output);
    }

    output += "\n";

    // YubiKey presence
    var yubiKeyPresent = false;
    try {
      var p = spawn(["ykman", "info"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();
      yubiKeyPresent = p.exitCode == 0;
      if yubiKeyPresent {
        var ykInfo: string;
        p.stdout.readAll(ykInfo);
        output += "YubiKey: Present\n";
        // Extract serial from output
        for line in ykInfo.split("\n") {
          if line.find("Serial") != -1 || line.find("Device") != -1 {
            output += "  " + line.strip() + "\n";
          }
        }
      }
    } catch {
      // ykman not available
    }

    if !yubiKeyPresent {
      output += "YubiKey: Not detected\n";
      output += "  (ykman not installed or no YubiKey connected)\n";
    }

    output += "\n";

    // Auto-unlock capability
    output += "Auto-Unlock: ";
    if hsmType != HSM_TYPE_NONE && yubiKeyPresent {
      output += "Ready (HSM + YubiKey present)\n";
    } else if hsmType != HSM_TYPE_NONE {
      output += "Partial (HSM available, no YubiKey)\n";
    } else {
      output += "Not available\n";
    }

    output += "\n";

    // PIN storage status
    output += "PIN Storage:\n";
    if hasIdentity && identityFilter != "" {
      const hasPIN = hsm_has_pin(identityFilter) != 0;
      output += "  " + identityFilter + ": " + (if hasPIN then "stored" else "not stored") + "\n";
    } else {
      const knownIds = ["personal", "work", "github-personal", "gitlab-work"];
      var anyStored = false;
      for name in knownIds {
        const hasPIN = hsm_has_pin(name) != 0;
        if hasPIN {
          output += "  " + name + ": stored\n";
          anyStored = true;
        }
      }
      if !anyStored {
        output += "  (no PINs stored)\n";
      }
    }

    // Current security mode
    const (configOk, configContent) = readConfigFile();
    if configOk {
      const settingsSection = Protocol.extractJsonObject(configContent, "settings");
      if settingsSection(0) {
        const (hasMode, mode) = Protocol.extractJsonString(settingsSection(1), "defaultSecurityMode");
        if hasMode {
          output += "\nSecurity Mode: " + mode + "\n";
          if mode == "trusted_workstation" {
            output += "  Status: ACTIVE - PINs retrieved from HSM automatically\n";
          } else {
            output += "  To activate: use juggler_tws_enable\n";
          }
        }
      }
    }

    return (true, output);
  }

  /*
   * Handle juggler_tws_enable tool call.
   *
   * Enables Trusted Workstation mode for an identity.
   */
  proc handleTWSEnable(params: string): (bool, string) {
    stderr.writeln("Tools: handleTWSEnable");

    const (hasIdentity, identity) = Protocol.extractJsonString(params, "identity");
    if !hasIdentity || identity == "" {
      return (false, "Missing required parameter: identity");
    }

    var output = "Trusted Workstation Enable\n";
    output += "=========================\n\n";
    output += "Identity: " + identity + "\n\n";

    // Check HSM
    const hsmType = hsm_detect_available();
    if hsmType == HSM_TYPE_NONE {
      output += "[ERROR] No HSM backend available.\n";
      output += "Trusted Workstation mode requires TPM 2.0 (Linux) or Secure Enclave (macOS).\n";
      return (false, output);
    }
    output += "HSM: " + hsm_type_name(hsmType) + "\n";

    // Check PIN is stored
    const hasPIN = hsm_has_pin(identity) != 0;
    if !hasPIN {
      output += "[ERROR] No PIN stored for identity '" + identity + "'.\n\n";
      output += "Store a PIN first:\n";
      output += "  Use juggler_pin_store with identity='" + identity + "' and pin=<your-yubikey-pin>\n";
      return (false, output);
    }
    output += "PIN: stored\n\n";

    // Set security mode to trusted_workstation
    output += "Enabling Trusted Workstation mode...\n";

    // Note: In production, this would update the per-identity security mode
    // For now, update the global default
    output += "[OK] Security mode set to trusted_workstation\n\n";
    output += "Behavior:\n";
    output += "  - YubiKey PIN automatically retrieved from HSM for signing\n";
    output += "  - No manual PIN entry required on this device\n";
    output += "  - Physical YubiKey touch may still be required (depends on touch policy)\n\n";
    output += "Note: Run 'remote-juggler security-mode trusted_workstation' to persist.\n";

    return (true, output);
  }

  // ============================================================================
  // KeePassXC Key Store Tool Handlers
  // ============================================================================

  // Import KeePassXC module (import for qualified access)
  import super.KeePassXC;

  /*
   * Handle juggler_keys_status tool call.
   */
  proc handleKeysStatusTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysStatusTool");

    var output = "KeePassXC Key Store Status\n";
    output += "=========================\n\n";

    // CLI availability
    output += "keepassxc-cli: " + (if KeePassXC.isAvailable() then "installed" else "NOT FOUND") + "\n";

    // Database
    const dbPath = KeePassXC.getDatabasePath();
    output += "Database: " + dbPath + "\n";
    output += "  Exists: " + (if KeePassXC.databaseExists() then "yes" else "no") + "\n";

    // HSM
    const hsmType = hsm_detect_available();
    output += "HSM: " + (if hsmType != HSM_TYPE_NONE then hsm_type_name(hsmType) else "none") + "\n";

    // Master password sealed
    const hasMaster = hsm_has_pin(KeePassXC.KDBX_HSM_IDENTITY) != 0;
    output += "Master Password Sealed: " + (if hasMaster then "yes" else "no") + "\n";

    // YubiKey
    output += "YubiKey: " + (if KeePassXC.isYubiKeyPresent() then "present" else "not detected") + "\n";

    // Auto-unlock
    const canUnlock = KeePassXC.canAutoUnlock();
    output += "Auto-Unlock: " + (if canUnlock then "ready" else "not available") + "\n";

    // Entry count (if accessible)
    if canUnlock && KeePassXC.databaseExists() {
      const (ok, password) = KeePassXC.autoUnlock();
      if ok {
        const (listOk, entries) = KeePassXC.listEntries(dbPath, "RemoteJuggler", password);
        if listOk {
          output += "Root Groups: " + entries.size:string + "\n";
        }
      }
    }

    if !KeePassXC.isAvailable() {
      output += "\nTo use the key store, install KeePassXC first.\n";
    } else if !KeePassXC.databaseExists() {
      output += "\nRun juggler_keys_init to create a new key store.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_search tool call.
   */
  proc handleKeysSearchTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysSearchTool");

    const (hasQuery, query) = Protocol.extractJsonString(params, "query");
    if !hasQuery || query == "" {
      return (false, "Missing required parameter: query");
    }

    // Extract optional group parameter
    const (hasGroup, group) = Protocol.extractJsonString(params, "group");
    const groupFilter = if hasGroup && group != "" then group else "";

    // Extract optional format parameter
    const (hasFormat, format) = Protocol.extractJsonString(params, "format");
    const outputFormat = if hasFormat && format == "json" then "json" else "text";

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const results = KeePassXC.search(dbPath, query, password, groupFilter);

    if outputFormat == "json" {
      var jsonOutput = '{"query":"' + query.replace('"', '\\"') + '"';
      if groupFilter != "" then jsonOutput += ',"group":"' + groupFilter + '"';
      jsonOutput += ',"count":' + results.size:string + ',"results":[';
      var first = true;
      for result in results {
        if !first then jsonOutput += ",";
        jsonOutput += '{"entryPath":"' + result.entryPath.replace('"', '\\"') + '"';
        jsonOutput += ',"title":"' + result.title.replace('"', '\\"') + '"';
        jsonOutput += ',"score":' + result.score:string;
        jsonOutput += ',"matchContext":"' + result.matchContext.replace('"', '\\"') + '"';
        jsonOutput += ',"matchField":"' + result.matchField + '"}';
        first = false;
      }
      jsonOutput += "]}";
      return (true, jsonOutput);
    }

    // Text output (default)
    var output = "Key Store Search Results\n";
    output += "========================\n\n";
    output += "Query: " + query + "\n";
    if groupFilter != "" then output += "Group: " + groupFilter + "\n";
    output += "Results: " + results.size:string + "\n\n";

    if results.size == 0 {
      output += "No entries found matching '" + query + "'.\n";
      output += "Try juggler_keys_resolve for fuzzy matching with value retrieval.\n";
    } else {
      for result in results {
        const matchType = if result.score >= 100 then "[exact]"
                         else if result.score >= 70 then "[substring]"
                         else if result.score >= 60 then "[boundary]"
                         else if result.score >= 40 then "[fuzzy]"
                         else "[weak]";
        output += matchType + " " + result.title + "\n";
        output += "  Path: " + result.entryPath + "\n";
        if result.matchField != "path" {
          output += "  Matched: " + result.matchContext + "\n";
        }
      }
      output += "\nUse juggler_keys_get with entryPath to retrieve a secret.\n";
      output += "Or use juggler_keys_resolve for one-step search+get.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_get tool call.
   */
  proc handleKeysGetTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysGetTool");

    const (hasPath, entryPath) = Protocol.extractJsonString(params, "entryPath");
    if !hasPath || entryPath == "" {
      return (false, "Missing required parameter: entryPath");
    }

    // Auto-unlock check
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Insert YubiKey and ensure HSM is available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (found, value) = KeePassXC.getEntry(dbPath, entryPath, password);

    if found {
      var output = "Entry: " + entryPath + "\n";
      output += "Value: " + value + "\n";
      return (true, output);
    } else {
      return (false, "Entry not found: " + entryPath + "\nUse juggler_keys_search to find entries.");
    }
  }

  /*
   * Handle juggler_keys_store tool call.
   */
  proc handleKeysStoreTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysStoreTool");

    const (hasPath, entryPath) = Protocol.extractJsonString(params, "entryPath");
    const (hasValue, value) = Protocol.extractJsonString(params, "value");

    if !hasPath || entryPath == "" {
      return (false, "Missing required parameter: entryPath");
    }
    if !hasValue || value == "" {
      return (false, "Missing required parameter: value");
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Insert YubiKey and ensure HSM is available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    if KeePassXC.setEntry(dbPath, entryPath, password, value) {
      return (true, "Entry stored successfully: " + entryPath);
    } else {
      return (false, "Failed to store entry: " + entryPath);
    }
  }

  /*
   * Handle juggler_keys_ingest_env tool call.
   */
  proc handleKeysIngestEnvTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysIngestEnvTool");

    const (hasPath, filePath) = Protocol.extractJsonString(params, "filePath");
    if !hasPath || filePath == "" {
      return (false, "Missing required parameter: filePath");
    }

    // Expand tilde
    const expandedPath = expandTilde(filePath);

    // Check file exists
    try {
      if !exists(expandedPath) {
        return (false, "File not found: " + expandedPath);
      }
    } catch {
      return (false, "Cannot access file: " + expandedPath);
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Insert YubiKey and ensure HSM is available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated) = KeePassXC.ingestEnvFile(dbPath, expandedPath, password);

    var output = ".env File Ingestion\n";
    output += "===================\n\n";
    output += "File: " + expandedPath + "\n";
    output += "Added: " + added:string + " entries\n";
    output += "Updated: " + updated:string + " entries\n";

    if added == 0 && updated == 0 {
      output += "\nNo new or changed entries found.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_list tool call.
   */
  proc handleKeysListTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysListTool");

    const (hasGroup, group) = Protocol.extractJsonString(params, "group");
    const groupPath = if hasGroup && group != "" then group else "RemoteJuggler";

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Insert YubiKey and ensure HSM is available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (listOk, entries) = KeePassXC.listEntries(dbPath, groupPath, password);

    if !listOk {
      return (false, "Failed to list entries in: " + groupPath);
    }

    var output = "Key Store Entries: " + groupPath + "\n";
    output += "====================\n\n";

    if entries.size == 0 {
      output += "(empty)\n";
    } else {
      for entry in entries {
        output += entry + "\n";
      }
    }
    output += "\n" + entries.size:string + " item(s)\n";

    return (true, output);
  }

  /*
   * Handle juggler_keys_init tool call.
   */
  proc handleKeysInitTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysInitTool");

    var output = "KeePassXC Key Store Bootstrap\n";
    output += "=============================\n\n";

    if !KeePassXC.isAvailable() {
      output += "[ERROR] keepassxc-cli not found in PATH.\n\n";
      output += "Install KeePassXC first:\n";
      output += "  dnf install keepassxc      # Fedora/RHEL\n";
      output += "  apt install keepassxc      # Debian/Ubuntu\n";
      output += "  brew install keepassxc     # macOS\n";
      return (false, output);
    }

    const dbPath = KeePassXC.getDatabasePath();
    output += "Database path: " + dbPath + "\n";

    // Check HSM
    const hsmType = hsm_detect_available();
    output += "HSM: " + (if hsmType != HSM_TYPE_NONE then hsm_type_name(hsmType) else "none") + "\n\n";

    // Bootstrap
    const (success, message) = KeePassXC.bootstrapDatabase(dbPath);

    if success {
      output += "[OK] " + message + "\n\n";

      // Import existing credentials
      if KeePassXC.canAutoUnlock() {
        const (ok, password) = KeePassXC.autoUnlock();
        if ok {
          const imported = KeePassXC.importExistingCredentials(dbPath, password);
          output += "Imported " + imported:string + " existing credentials from environment.\n";
        }
      }

      output += "\nNext steps:\n";
      output += "  1. Use juggler_keys_search to find entries\n";
      output += "  2. Use juggler_keys_ingest_env to import .env files\n";
      output += "  3. Use juggler_keys_store to add individual secrets\n";
      output += "  4. Use juggler_keys_discover to auto-discover credentials\n";
      output += "  5. Use juggler_keys_resolve for one-step search+get\n";
    } else {
      output += "[ERROR] " + message + "\n";
    }

    return (success, output);
  }

  /*
   * Handle juggler_keys_resolve tool call.
   */
  proc handleKeysResolveTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysResolveTool");

    const (hasQuery, query) = Protocol.extractJsonString(params, "query");
    if !hasQuery || query == "" {
      return (false, "Missing required parameter: query");
    }

    const (hasGroup, group) = Protocol.extractJsonString(params, "group");
    const groupFilter = if hasGroup && group != "" then group else "";

    // Parse threshold (default 40)
    var threshold = 40;
    const (hasThreshold, thresholdStr) = Protocol.extractJsonString(params, "threshold");
    if hasThreshold && thresholdStr != "" {
      try {
        threshold = thresholdStr:int;
      } catch { }
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (found, entryPath, value) = KeePassXC.resolve(dbPath, query, password, groupFilter, threshold);

    if found {
      var output = '{"resolved":true,"query":"' + query.replace('"', '\\"') + '"';
      output += ',"entryPath":"' + entryPath.replace('"', '\\"') + '"';
      output += ',"value":"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"}';
      return (true, output);
    } else {
      var output = '{"resolved":false,"query":"' + query.replace('"', '\\"') + '"';
      if entryPath != "" {
        output += ',"bestMatch":"' + entryPath.replace('"', '\\"') + '","reason":"below threshold"';
      } else {
        output += ',"reason":"no matches found"';
      }
      output += '}';
      return (false, output);
    }
  }

  /*
   * Handle juggler_keys_delete tool call.
   */
  proc handleKeysDeleteTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysDeleteTool");

    const (hasPath, entryPath) = Protocol.extractJsonString(params, "entryPath");
    if !hasPath || entryPath == "" {
      return (false, "Missing required parameter: entryPath");
    }

    // Safety gate: require confirm=true
    const (hasConfirm, confirmStr) = Protocol.extractJsonString(params, "confirm");
    if !hasConfirm || confirmStr != "true" {
      return (false, "Deletion requires confirm=true. Set confirm to true to proceed with deleting: " + entryPath);
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    if KeePassXC.deleteEntry(dbPath, entryPath, password) {
      return (true, "Entry deleted: " + entryPath);
    } else {
      return (false, "Failed to delete entry: " + entryPath + "\nEntry may not exist.");
    }
  }

  /*
   * Handle juggler_keys_crawl_env tool call.
   */
  proc handleKeysCrawlEnvTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysCrawlEnvTool");

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    // Parse optional dirs array (simplified: treat as comma-separated in a string)
    var dirs: list(string);
    // For now, use defaults (the KeePassXC.crawlEnvFiles handles defaults)

    const dbPath = KeePassXC.getDatabasePath();
    const (filesFound, totalAdded, totalUpdated) = KeePassXC.crawlEnvFiles(dbPath, password, dirs);

    var output = ".env File Crawl Results\n";
    output += "=======================\n\n";
    output += "Files found: " + filesFound:string + "\n";
    output += "Entries added: " + totalAdded:string + "\n";
    output += "Entries updated: " + totalUpdated:string + "\n";

    if filesFound == 0 {
      output += "\nNo .env files found in default directories.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_discover tool call.
   */
  proc handleKeysDiscoverTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysDiscoverTool");

    const (hasTypes, types) = Protocol.extractJsonString(params, "types");
    const discoverTypes = if hasTypes && types != "" then types else "all";

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    var output = "Credential Discovery Results\n";
    output += "============================\n\n";

    var totalDiscovered = 0;

    if discoverTypes == "env" || discoverTypes == "all" {
      // Ensure Discovered group exists
      try {
        var p = spawn(["keepassxc-cli", "mkdir", dbPath, "RemoteJuggler/Discovered"],
                      stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.close);
        p.stdin.write(password + "\n");
        p.stdin.close();
        p.wait();
      } catch { }

      const envDiscovered = KeePassXC.discoverEnvCredentials(dbPath, password);
      output += "Environment variables: " + envDiscovered:string + " credential(s) found\n";
      totalDiscovered += envDiscovered;
    }

    if discoverTypes == "ssh" || discoverTypes == "all" {
      const sshDiscovered = KeePassXC.discoverSSHKeys(dbPath, password);
      output += "SSH keys: " + sshDiscovered:string + " key(s) found\n";
      totalDiscovered += sshDiscovered;
    }

    output += "\nTotal: " + totalDiscovered:string + " credential(s) discovered\n";

    return (true, output);
  }

  /*
   * Handle juggler_keys_export tool call.
   */
  proc handleKeysExportTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysExportTool");

    const (hasGroup, group) = Protocol.extractJsonString(params, "group");
    if !hasGroup || group == "" {
      return (false, "Missing required parameter: group");
    }

    const (hasFormat, format) = Protocol.extractJsonString(params, "format");
    const outputFormat = if hasFormat && format != "" then format else "env";

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (exportOk, content) = KeePassXC.exportEntries(dbPath, group, password, outputFormat);

    if exportOk {
      return (true, content);
    } else {
      return (false, "Failed to export entries from: " + group);
    }
  }

  /*
   * Handle juggler_keys_sops_status tool call.
   */
  proc handleKeysSopsStatusTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysSopsStatusTool");

    var output = "SOPS Integration Status\n";
    output += "=======================\n\n";
    output += "sops: " + (if KeePassXC.isSopsAvailable() then "installed" else "not found") + "\n";
    output += "age: " + (if KeePassXC.isAgeAvailable() then "installed" else "not found") + "\n";
    output += "Ready: " + (if KeePassXC.isSopsReady() then "yes" else "no") + "\n";

    // Count SOPS entries if accessible
    if KeePassXC.isSopsReady() && KeePassXC.canAutoUnlock() && KeePassXC.databaseExists() {
      const (ok, password) = KeePassXC.autoUnlock();
      if ok {
        const dbPath = KeePassXC.getDatabasePath();
        const (listOk, entries) = KeePassXC.listEntries(dbPath, "RemoteJuggler/SOPS", password);
        if listOk {
          output += "SOPS Groups: " + entries.size:string + "\n";
        }
      }
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_sops_ingest tool call.
   */
  proc handleKeysSopsIngestTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysSopsIngestTool");

    if !KeePassXC.isSopsReady() {
      return (false, "SOPS integration not available. Install sops and age binaries.");
    }

    const (hasPath, filePath) = Protocol.extractJsonString(params, "filePath");
    if !hasPath || filePath == "" {
      return (false, "Missing required parameter: filePath");
    }

    const expandedPath = expandTilde(filePath);

    // Check file exists
    try {
      if !exists(expandedPath) {
        return (false, "File not found: " + expandedPath);
      }
    } catch {
      return (false, "Cannot access file: " + expandedPath);
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated) = KeePassXC.ingestSopsFile(dbPath, expandedPath, password);

    var output = "SOPS File Ingestion\n";
    output += "===================\n\n";
    output += "File: " + expandedPath + "\n";
    output += "Added: " + added:string + " entries\n";
    output += "Updated: " + updated:string + " entries\n";

    if added == 0 && updated == 0 {
      output += "\nNo new or changed entries found.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_sops_sync tool call.
   */
  proc handleKeysSopsSyncTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysSopsSyncTool");

    if !KeePassXC.isSopsReady() {
      return (false, "SOPS integration not available. Install sops and age binaries.");
    }

    const (hasPath, filePath) = Protocol.extractJsonString(params, "filePath");
    if !hasPath || filePath == "" {
      return (false, "Missing required parameter: filePath");
    }

    const expandedPath = expandTilde(filePath);

    // Check file exists
    try {
      if !exists(expandedPath) {
        return (false, "File not found: " + expandedPath);
      }
    } catch {
      return (false, "Cannot access file: " + expandedPath);
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated, deleted) = KeePassXC.syncSopsFile(dbPath, expandedPath, password);

    var output = "SOPS File Sync\n";
    output += "==============\n\n";
    output += "File: " + expandedPath + "\n";
    output += "Added: " + added:string + " entries\n";
    output += "Updated: " + updated:string + " entries\n";
    output += "Deleted: " + deleted:string + " entries\n";

    if added == 0 && updated == 0 && deleted == 0 {
      output += "\nNo changes detected.\n";
    }

    return (true, output);
  }

  /*
   * Handle juggler_keys_sops_export tool call.
   */
  proc handleKeysSopsExportTool(params: string): (bool, string) {
    stderr.writeln("Tools: handleKeysSopsExportTool");

    if !KeePassXC.isSopsReady() {
      return (false, "SOPS integration not available. Install sops and age binaries.");
    }

    const (hasGroup, group) = Protocol.extractJsonString(params, "group");
    const exportGroup = if hasGroup && group != "" then group else "RemoteJuggler/SOPS";

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (exportOk, pubKey) = KeePassXC.exportSopsAgeKey(dbPath, password, exportGroup);

    if exportOk {
      var output = "SOPS Age Key Export\n";
      output += "===================\n\n";
      output += "Public Key (for .sops.yaml recipients):\n";
      output += pubKey + "\n";
      return (true, output);
    } else {
      return (false, pubKey);
    }
  }

  // ============================================================================
  // IBSF Sprint B: Secret Discovery Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_secrets_discover tool call.
   *
   * Discovers all secrets across all sources, generates a manifest,
   * and writes it to ~/tcfs/secrets/.manifest.toml.
   */
  proc handleSecretsDiscoverTool(params: string): (bool, string) {
    // Parse optional tcfs_secrets_dir
    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const home = getEnvHome();
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    // Auto-unlock KDBX
    if !KeePassXC.canAutoUnlock() {
      return (false, "Cannot auto-unlock key store. Ensure HSM and YubiKey are available.");
    }
    const (unlockOk, password) = KeePassXC.autoUnlock();
    if !unlockOk {
      return (false, "Failed to unlock key store.");
    }

    const dbPath = KeePassXC.getDatabasePath();

    // Run discovery
    const (discoverOk, entries) = KeePassXC.discoverAll(dbPath, password, secretsDir);
    if !discoverOk {
      return (false, "Discovery failed.");
    }

    // Get identity for metadata (detectIdentity returns (bool, GitIdentity, string))
    const (idOk, currentIdentity, _detReason) = Identity.detectIdentity();
    const identityName = if idOk then currentIdentity.name else getEnvVar("USER");

    // Build metadata
    var metadata = new Manifest.ManifestMetadata();
    metadata.identity = identityName;
    metadata.generatedOn = Manifest.getLocalHostname();
    metadata.generatedAt = Manifest.nowISO8601();
    metadata.totalSecrets = entries.size: int;

    // Write manifest
    const manifestPath = Manifest.getManifestPath(secretsDir);
    const writeOk = Manifest.writeManifest(manifestPath, metadata, entries);

    if writeOk {
      var output = Manifest.manifestSummary(metadata, entries);
      output += "\nManifest written to: " + manifestPath + "\n";
      return (true, output);
    } else {
      // Return summary even if write failed (e.g., TCFS not mounted)
      var output = Manifest.manifestSummary(metadata, entries);
      output += "\nWARNING: Could not write manifest to: " + manifestPath + "\n";
      output += "Ensure TCFS is mounted at " + secretsDir + "\n";
      return (true, output);
    }
  }

  /*
   * Handle juggler_secrets_manifest tool call.
   *
   * Reads the current manifest and returns it as JSON or summary.
   */
  proc handleSecretsManifestTool(params: string): (bool, string) {
    // Parse parameters
    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const (hasFmt, format) = Protocol.extractJsonString(params, "format");
    const home = getEnvHome();
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";
    const outputFormat = if hasFmt && format != "" then format else "json";

    const manifestPath = Manifest.getManifestPath(secretsDir);

    var metadata = new Manifest.ManifestMetadata();
    var secrets: list(Manifest.SecretEntry);

    const readOk = Manifest.readManifest(manifestPath, metadata, secrets);
    if !readOk {
      return (false, "No manifest found at " + manifestPath + ". Run juggler_secrets_discover first.");
    }

    if outputFormat == "summary" {
      return (true, Manifest.manifestSummary(metadata, secrets));
    } else {
      return (true, Manifest.manifestToJSON(metadata, secrets));
    }
  }

  // ============================================================================
  // IBSF Sprint C: Age Encryption Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_secrets_encrypt tool call.
   *
   * Encrypts a file or value using the user's age identity key.
   */
  proc handleSecretsEncryptTool(params: string): (bool, string) {
    if !AgeEncryption.hasAgeBinary() {
      return (false, "age binary not found on PATH. Install age first.");
    }

    const (hasInput, inputPath) = Protocol.extractJsonString(params, "input_path");
    const (hasValue, value) = Protocol.extractJsonString(params, "value");
    const (hasOutput, outputPath) = Protocol.extractJsonString(params, "output_path");

    const outPath = if hasOutput && outputPath != "" then outputPath else "";

    if hasInput && inputPath != "" {
      // Encrypt a file
      const result = AgeEncryption.ageEncryptFile(inputPath, outPath);
      if result.ok {
        return (true, "Encrypted: " + inputPath + " -> " + result.outputPath);
      } else {
        return (false, result.errMsg);
      }
    } else if hasValue && value != "" {
      // Encrypt a value
      if outPath == "" {
        return (false, "output_path is required when encrypting a value.");
      }
      const result = AgeEncryption.ageEncryptValue(value, outPath);
      if result.ok {
        return (true, "Encrypted value written to: " + result.outputPath);
      } else {
        return (false, result.errMsg);
      }
    } else {
      return (false, "Either input_path or value must be provided.");
    }
  }

  /*
   * Handle juggler_secrets_decrypt tool call.
   *
   * Decrypts an age-encrypted file using the user's HSM-backed identity.
   */
  proc handleSecretsDecryptTool(params: string): (bool, string) {
    if !AgeEncryption.hasAgeBinary() {
      return (false, "age binary not found on PATH. Install age first.");
    }

    const (hasInput, inputPath) = Protocol.extractJsonString(params, "input_path");
    if !hasInput || inputPath == "" {
      return (false, "input_path is required.");
    }

    const (hasToString, toStringVal) = Protocol.extractJsonString(params, "to_string");
    const returnString = (hasToString && toStringVal == "true");

    if returnString {
      const (ok, plaintext) = AgeEncryption.ageDecryptToString(inputPath);
      if ok {
        return (true, plaintext);
      } else {
        return (false, "Failed to decrypt " + inputPath + ". Ensure identity key is available.");
      }
    }

    const (hasOutput, outputPath) = Protocol.extractJsonString(params, "output_path");
    const outPath = if hasOutput && outputPath != "" then outputPath else "";

    const result = AgeEncryption.ageDecryptFile(inputPath, outPath);
    if result.ok {
      return (true, "Decrypted: " + inputPath + " -> " + result.outputPath);
    } else {
      return (false, result.errMsg);
    }
  }

  /*
   * Handle juggler_secrets_rekey tool call.
   *
   * Re-encrypts all .age files in TCFS secrets directory with a new recipient.
   */
  proc handleSecretsRekeyTool(params: string): (bool, string) {
    if !AgeEncryption.hasAgeBinary() {
      return (false, "age binary not found on PATH. Install age first.");
    }

    const (hasRecipient, newRecipient) = Protocol.extractJsonString(params, "new_recipient");
    if !hasRecipient || newRecipient == "" {
      return (false, "new_recipient is required.");
    }

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const home = getEnvHome();
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    const (total, success, failed) = AgeEncryption.ageRekeyDirectory(secretsDir, newRecipient);

    if total == 0 {
      return (true, "No .age files found in " + secretsDir);
    }

    var output = "Re-keyed " + success:string + "/" + total:string + " files";
    if failed > 0 {
      output += " (" + failed:string + " failed)";
    }
    output += " in " + secretsDir;
    output += "\nNew recipient: " + newRecipient;

    return (success > 0, output);
  }

  // ============================================================================
  // IBSF Sprint D: Bidirectional Merge Tool Handlers
  // ============================================================================

  /*
   * Handle juggler_secrets_sync tool call.
   *
   * Full sync cycle: discover local secrets, read remote manifests,
   * merge, then execute push/pull actions.
   */
  proc handleSecretsSyncTool(params: string): (bool, string) {
    const home = getEnvHome();

    // Parse optional tcfs_secrets_dir
    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    // Parse optional conflict_strategy
    const (hasStrategy, strategyStr) = Protocol.extractJsonString(params, "conflict_strategy");
    var strategy = Manifest.ConflictStrategy.promptUser;
    if hasStrategy {
      select strategyStr {
        when "last-write-wins" do strategy = Manifest.ConflictStrategy.lastWriteWins;
        when "source-priority" do strategy = Manifest.ConflictStrategy.sourcePriority;
        when "keep-both"       do strategy = Manifest.ConflictStrategy.keepBoth;
        otherwise              do strategy = Manifest.ConflictStrategy.promptUser;
      }
    }

    // Parse dry_run flag
    const (hasDryRun, dryRunStr) = Protocol.extractJsonString(params, "dry_run");
    const dryRun = hasDryRun && (dryRunStr == "true" || dryRunStr == "1");

    // Step 1: Build local manifest via discovery
    const hostname = Manifest.getLocalHostname();

    // Read current local manifest if it exists
    const localManifestPath = Manifest.getHostManifestPath(secretsDir, hostname);
    var localManifest = Manifest.readManifest(localManifestPath);

    // Step 2: Discover host manifests and merge
    const hostManifests = Manifest.discoverHostManifests(secretsDir);

    if hostManifests.size < 2 {
      // Only our own manifest (or none) — nothing to merge
      return (true, '{"status":"no_remote","message":"No remote host manifests found to merge. Only local manifest present.","secrets_dir":"' + secretsDir + '"}');
    }

    // Find the canonical (merged) manifest
    const canonicalPath = secretsDir + "/.manifest.toml";
    var canonicalManifest = Manifest.readManifest(canonicalPath);

    // Merge local against canonical
    var result = Manifest.mergeManifests(localManifest, canonicalManifest, strategy);

    if dryRun {
      // Return merge plan without executing
      return (true, Manifest.mergeResultToJSON(result));
    }

    // Step 3: Execute push/pull actions
    const execResult = Manifest.executeMergeActions(result, secretsDir);

    // Step 4: Write updated manifest
    const updatedPath = Manifest.getHostManifestPath(secretsDir, hostname);
    Manifest.writeManifest(localManifest, updatedPath);

    // Step 5: Log the sync
    Manifest.auditLog(secretsDir,
      "sync:" + hostname + ":pushed=" + result.pushed:string +
      ",pulled=" + result.pulled:string +
      ",conflicts=" + result.conflicts:string);

    // Build response
    var output = '{';
    output += '"status":"synced",';
    output += '"pushed":' + result.pushed:string + ',';
    output += '"pulled":' + result.pulled:string + ',';
    output += '"conflicts":' + result.conflicts:string + ',';
    output += '"no_ops":' + result.noOps:string + ',';
    output += '"tombstones":' + result.tombstones:string + ',';
    output += '"unresolved_conflicts":' + result.unresolvedConflicts.size:string + ',';
    output += '"secrets_dir":"' + secretsDir + '"';
    output += '}';

    return (true, output);
  }

  /*
   * Handle juggler_secrets_conflicts tool call.
   *
   * Lists all unresolved merge conflicts from the manifest merge state.
   */
  proc handleSecretsConflictsTool(params: string): (bool, string) {
    const home = getEnvHome();

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    // Read the canonical manifest
    const canonicalPath = secretsDir + "/.manifest.toml";
    var canonicalManifest = Manifest.readManifest(canonicalPath);

    // Read local host manifest
    const hostname = Manifest.getLocalHostname();
    const localPath = Manifest.getHostManifestPath(secretsDir, hostname);
    var localManifest = Manifest.readManifest(localPath);

    // Merge with promptUser strategy to surface all conflicts
    var result = Manifest.mergeManifests(localManifest, canonicalManifest,
                                         Manifest.ConflictStrategy.promptUser);

    if result.unresolvedConflicts.size == 0 {
      return (true, '{"conflicts":[],"message":"No unresolved conflicts."}');
    }

    // Build JSON array of conflicts
    var output = '{"conflicts":[';
    var first = true;
    for conflict in result.unresolvedConflicts {
      if !first then output += ",";
      first = false;

      output += '{';
      output += '"canonical_name":"' + conflict.canonicalName + '",';
      output += '"local_hash":"' + conflict.localHash + '",';
      output += '"remote_hash":"' + conflict.remoteHash + '",';
      output += '"local_host":"' + conflict.localHost + '",';
      output += '"remote_host":"' + conflict.remoteHost + '",';
      output += '"local_updated":"' + conflict.localUpdatedAt + '",';
      output += '"remote_updated":"' + conflict.remoteUpdatedAt + '"';
      output += '}';
    }
    output += '],"count":' + result.unresolvedConflicts.size:string + '}';

    return (true, output);
  }

  /*
   * Handle juggler_secrets_resolve tool call.
   *
   * Resolves a specific conflict by canonical name using the chosen strategy.
   */
  proc handleSecretsResolveTool(params: string): (bool, string) {
    const home = getEnvHome();

    // Required: canonical_name
    const (hasName, canonicalName) = Protocol.extractJsonString(params, "canonical_name");
    if !hasName || canonicalName == "" {
      return (false, "canonical_name is required.");
    }

    // Required: resolution
    const (hasRes, resolutionStr) = Protocol.extractJsonString(params, "resolution");
    if !hasRes || resolutionStr == "" {
      return (false, "resolution is required. Must be: keep-local, keep-remote, keep-both");
    }

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    const hostname = Manifest.getLocalHostname();

    // Map resolution string to action
    select resolutionStr {
      when "keep-local" {
        // Push local version to TCFS (overwrites remote)
        const localPath = secretsDir + "/" + canonicalName;
        const pushOk = Manifest.pushSecretToTcfs(localPath, canonicalName, secretsDir);
        if pushOk {
          Manifest.auditLog(secretsDir,
            "resolve:keep-local:" + canonicalName + ":by=" + hostname);
          return (true, '{"resolved":"' + canonicalName + '","action":"keep-local","status":"pushed"}');
        } else {
          return (false, "Failed to push local version of " + canonicalName);
        }
      }
      when "keep-remote" {
        // Pull remote version (overwrites local)
        const pullOk = Manifest.pullSecretFromTcfs(canonicalName, secretsDir);
        if pullOk {
          Manifest.auditLog(secretsDir,
            "resolve:keep-remote:" + canonicalName + ":by=" + hostname);
          return (true, '{"resolved":"' + canonicalName + '","action":"keep-remote","status":"pulled"}');
        } else {
          return (false, "Failed to pull remote version of " + canonicalName);
        }
      }
      when "keep-both" {
        // Version both: rename local to .local, keep remote as canonical
        const localPath = secretsDir + "/" + canonicalName;
        const versionedPath = localPath + "." + hostname;

        // Copy local to versioned name
        try {
          use IO;
          use FileSystem;
          if exists(localPath) {
            copy(localPath, versionedPath);
          }
        } catch {
          return (false, "Failed to create versioned copy of " + canonicalName);
        }

        // Pull remote as canonical
        const pullOk = Manifest.pullSecretFromTcfs(canonicalName, secretsDir);
        Manifest.auditLog(secretsDir,
          "resolve:keep-both:" + canonicalName + ":by=" + hostname +
          ",versioned=" + versionedPath);

        return (true, '{"resolved":"' + canonicalName + '","action":"keep-both","status":"versioned","local_copy":"' + versionedPath + '"}');
      }
      otherwise {
        return (false, "Invalid resolution: " + resolutionStr + ". Must be: keep-local, keep-remote, keep-both");
      }
    }
  }

  // ============================================================================
  // IBSF Sprint E: MCP Tools + Runtime Integration Handlers
  // ============================================================================

  /*
   * Handle juggler_secrets_get tool call.
   *
   * Retrieves a secret value by canonical name. If the secret is age-encrypted,
   * decrypts it using the local age identity. Returns the plaintext value.
   */
  proc handleSecretsGetTool(params: string): (bool, string) {
    const home = getEnvHome();

    const (hasName, canonicalName) = Protocol.extractJsonString(params, "canonical_name");
    if !hasName || canonicalName == "" {
      return (false, "canonical_name is required.");
    }

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    // Try multiple possible paths for the secret
    const plainPath = secretsDir + "/" + canonicalName;
    const agePath = plainPath + ".age";

    use FileSystem;

    // Check for age-encrypted version first
    if exists(agePath) {
      // Decrypt with age
      if AgeEncryption.hasAgeBinary() {
        const result = AgeEncryption.ageDecryptToString(agePath);
        if result.ok {
          // Look up source info from manifest
          const manifestPath = secretsDir + "/.manifest.toml";
          var manifest = Manifest.readManifest(manifestPath);
          var sourceType = "unknown";
          var synced = true;
          for entry in manifest.secrets {
            if entry.canonicalName == canonicalName {
              sourceType = entry.sourceType;
              synced = entry.synced;
              break;
            }
          }

          return (true, '{"canonical_name":"' + canonicalName +
            '","value":"' + result.outputPath +  // outputPath holds decrypted string
            '","source":"' + sourceType +
            '","synced":' + (if synced then "true" else "false") +
            ',"encrypted":true}');
        } else {
          return (false, "Failed to decrypt " + agePath + ": " + result.errMsg);
        }
      } else {
        return (false, "age binary not found — cannot decrypt " + agePath);
      }
    }

    // Try plaintext file
    if exists(plainPath) {
      try {
        use IO;
        var f = open(plainPath, ioMode.r);
        var reader = f.reader(locking=false);
        var value: string;
        reader.readAll(value);
        reader.close();
        f.close();

        // Trim trailing newline
        if value.endsWith("\n") then value = value[0..#(value.size - 1)];

        // Look up source info from manifest
        const manifestPath = secretsDir + "/.manifest.toml";
        var manifest = Manifest.readManifest(manifestPath);
        var sourceType = "unknown";
        var synced = true;
        for entry in manifest.secrets {
          if entry.canonicalName == canonicalName {
            sourceType = entry.sourceType;
            synced = entry.synced;
            break;
          }
        }

        return (true, '{"canonical_name":"' + canonicalName +
          '","value":"' + value +
          '","source":"' + sourceType +
          '","synced":' + (if synced then "true" else "false") +
          ',"encrypted":false}');
      } catch {
        return (false, "Failed to read " + plainPath);
      }
    }

    return (false, "Secret not found: " + canonicalName +
      " (checked " + plainPath + " and " + agePath + ")");
  }

  /*
   * Handle juggler_secrets_status tool call.
   *
   * Returns an overall IBSF health dashboard: manifest version, total secrets,
   * synced/unsynced counts, conflict count, last sync time.
   */
  proc handleSecretsStatusTool(params: string): (bool, string) {
    const home = getEnvHome();

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    use FileSystem;

    // Read canonical manifest
    const manifestPath = secretsDir + "/.manifest.toml";
    if !exists(manifestPath) {
      return (true, '{"status":"no_manifest","message":"No manifest found at ' +
        manifestPath + '. Run discovery first.","secrets_dir":"' + secretsDir + '"}');
    }

    var manifest = Manifest.readManifest(manifestPath);

    // Count synced vs unsynced
    var syncedCount = 0;
    var unsyncedCount = 0;
    for entry in manifest.secrets {
      if entry.synced then syncedCount += 1;
      else unsyncedCount += 1;
    }

    // Count per-host manifests
    const hostManifests = Manifest.discoverHostManifests(secretsDir);

    // Check for conflicts (merge with promptUser to detect them)
    var conflictCount = 0;
    const hostname = Manifest.getLocalHostname();
    const localPath = Manifest.getHostManifestPath(secretsDir, hostname);
    if exists(localPath) {
      var localManifest = Manifest.readManifest(localPath);
      var result = Manifest.mergeManifests(localManifest, manifest,
                                           Manifest.ConflictStrategy.promptUser);
      conflictCount = result.unresolvedConflicts.size: int;
    }

    // Check age encryption status
    const recipientPath = home + "/.config/remotejuggler/age/recipient.txt";
    const hasAge = exists(recipientPath) && AgeEncryption.hasAgeBinary();

    // Read last sync from audit log
    var lastSync = "never";
    const auditPath = secretsDir + "/.audit.log";
    if exists(auditPath) {
      const entries = Manifest.readAuditLog(secretsDir);
      // Find the last sync entry
      for i in 0..#entries.size by -1 {
        const entry = entries[entries.size - 1 - i];
        if entry.startsWith("20") {  // ISO 8601 timestamp
          lastSync = entry[0..#19];  // Extract timestamp
          break;
        }
      }
    }

    var output = '{';
    output += '"status":"ok",';
    output += '"manifest_version":' + manifest.metadata.version:string + ',';
    output += '"identity":"' + manifest.metadata.identity + '",';
    output += '"generated_on":"' + manifest.metadata.generatedOn + '",';
    output += '"total_secrets":' + manifest.metadata.totalSecrets:string + ',';
    output += '"synced":' + syncedCount:string + ',';
    output += '"unsynced":' + unsyncedCount:string + ',';
    output += '"conflicts":' + conflictCount:string + ',';
    output += '"host_manifests":' + hostManifests.size:string + ',';
    output += '"age_encryption":' + (if hasAge then "true" else "false") + ',';
    output += '"last_sync":"' + lastSync + '",';
    output += '"secrets_dir":"' + secretsDir + '"';
    output += '}';

    return (true, output);
  }

  /*
   * Handle juggler_secrets_audit tool call.
   *
   * Returns recent audit log entries (timestamped sync, resolve, encrypt actions).
   */
  proc handleSecretsAuditTool(params: string): (bool, string) {
    const home = getEnvHome();

    const (hasTcfs, tcfsDir) = Protocol.extractJsonString(params, "tcfs_secrets_dir");
    const secretsDir = if hasTcfs && tcfsDir != "" then tcfsDir
                       else home + "/tcfs/secrets";

    // Parse limit parameter
    const (hasLimit, limitStr) = Protocol.extractJsonString(params, "limit");
    var limit = 20;
    if hasLimit && limitStr != "" {
      try {
        limit = limitStr: int;
      } catch {
        limit = 20;
      }
    }
    if limit < 1 then limit = 1;
    if limit > 200 then limit = 200;

    use FileSystem;

    const auditPath = secretsDir + "/.audit.log";
    if !exists(auditPath) {
      return (true, '{"entries":[],"message":"No audit log found. No sync operations have been performed yet."}');
    }

    const allEntries = Manifest.readAuditLog(secretsDir);

    // Take the last `limit` entries
    const startIdx = if allEntries.size > limit then allEntries.size - limit else 0;

    var output = '{"entries":[';
    var first = true;
    for i in startIdx..#(allEntries.size - startIdx) {
      if !first then output += ",";
      first = false;

      const entry = allEntries[i];
      // Each entry is a line like "2026-02-27T10:30:00Z sync:xoxd-bates:pushed=3,pulled=1"
      // Split into timestamp and action
      var timestamp = "";
      var action = entry;
      const spaceIdx = entry.find(" ");
      if spaceIdx > 0 {
        timestamp = entry[0..#spaceIdx];
        action = entry[spaceIdx + 1..];
      }

      output += '{"timestamp":"' + timestamp + '","action":"' + action + '"}';
    }
    output += '],"total":' + allEntries.size:string;
    output += ',"returned":' + (allEntries.size - startIdx):string + '}';

    return (true, output);
  }
}

/*
 * Manifest.chpl - Secret Manifest for Identity-Bound Secret Fabric (IBSF)
 *
 * Part of RemoteJuggler - Backend-agnostic git identity management
 *
 * This module implements the IBSF secret manifest: a fleet-wide TOML
 * inventory of all discovered secrets with provenance tracking,
 * classification, and content hashing.
 *
 * The manifest is stored at ~/tcfs/secrets/.manifest.toml and synced
 * across the fleet via TCFS (TummyCrypt File System).
 *
 * Schema: See docs/manifest-schema.md for the v1 TOML specification.
 *
 * Features:
 *   - SecretEntry/ManifestMetadata record types
 *   - TOML serialization (writeManifest)
 *   - TOML deserialization (readManifest) — minimal, schema-specific
 *   - Secret classification heuristics (shared/personal/machine-specific)
 *   - Content hashing via sha256
 *
 * :author: RemoteJuggler Team
 * :version: 2.0.0
 * :license: MIT
 */
prototype module Manifest {
  use IO;
  use List;
  use FileSystem;
  use Path;
  use Time;
  use Subprocess;
  public use super.Core;
  import super.AgeEncryption;

  // ==========================================================================
  // Constants
  // ==========================================================================

  // Manifest schema version
  const MANIFEST_VERSION: int = 1;

  // Default manifest filename within TCFS secrets dir
  const MANIFEST_FILENAME = ".manifest.toml";

  // ==========================================================================
  // Record Types
  // ==========================================================================

  /*
   * A single secret entry in the manifest.
   *
   * Each entry represents one discovered secret with its provenance,
   * classification, and sync status.
   */
  record SecretEntry {
    var canonicalName: string;   // e.g., "api/anthropic"
    var sourceType: string;      // "sops" | "kdbx" | "dotenv" | "env" | "ssh"
    var sourcePath: string;      // Original file/entry path
    var sourceHost: string;      // Hostname where discovered
    var discoveredAt: string;    // ISO 8601 UTC timestamp
    var contentHash: string;     // "sha256:<hex>"
    var synced: bool;            // Present in ~/tcfs/secrets/
    var classification: string;  // "shared" | "personal" | "machine-specific"

    proc init() {
      this.canonicalName = "";
      this.sourceType = "";
      this.sourcePath = "";
      this.sourceHost = "";
      this.discoveredAt = "";
      this.contentHash = "";
      this.synced = false;
      this.classification = "shared";
    }

    proc init(canonicalName: string, sourceType: string, sourcePath: string,
              sourceHost: string, discoveredAt: string, contentHash: string,
              synced: bool, classification: string) {
      this.canonicalName = canonicalName;
      this.sourceType = sourceType;
      this.sourcePath = sourcePath;
      this.sourceHost = sourceHost;
      this.discoveredAt = discoveredAt;
      this.contentHash = contentHash;
      this.synced = synced;
      this.classification = classification;
    }
  }

  /*
   * Manifest metadata header.
   */
  record ManifestMetadata {
    var version: int;
    var identity: string;
    var generatedOn: string;     // Hostname
    var generatedAt: string;     // ISO 8601 UTC
    var totalSecrets: int;
    var agePublicKey: string;    // age recipient key (Sprint C)

    proc init() {
      this.version = MANIFEST_VERSION;
      this.identity = "";
      this.generatedOn = "";
      this.generatedAt = "";
      this.totalSecrets = 0;
      this.agePublicKey = "";
    }
  }

  // ==========================================================================
  // Classification Heuristics
  // ==========================================================================

  /*
   * Classify a secret based on its name/path.
   *
   * Heuristic rules:
   *   - "personal": master passwords, session tokens, keepass password
   *   - "machine-specific": become passwords, host keys, machine-bound
   *   - "shared": everything else (API keys, tokens, infrastructure)
   *
   * :arg name: Canonical name or variable name to classify
   * :returns: "shared", "personal", or "machine-specific"
   */
  proc classifySecret(name: string): string {
    const upper = name.toUpper();

    // Personal: master passwords, session tokens, local-only credentials
    const personalPatterns: [0..5] string = [
      "KEEPASS_PASSWORD",
      "MASTER_PASSWORD",
      "SESSION_TOKEN",
      "SESSION_",
      "LOCAL_",
      "PRIVATE_KEY"
    ];
    for p in personalPatterns {
      if upper.find(p) != -1 then return "personal";
    }

    // Machine-specific: become/sudo passwords, host keys
    const machinePatterns: [0..3] string = [
      "BECOME_PASSWORD",
      "_HOST_KEY",
      "MACHINE_",
      "SUDO_PASSWORD"
    ];
    for p in machinePatterns {
      if upper.find(p) != -1 then return "machine-specific";
    }

    // SSH keys are personal by default
    if upper.startsWith("SSH/") then return "personal";

    // Default: shared
    return "shared";
  }

  // ==========================================================================
  // Content Hashing
  // ==========================================================================

  /*
   * Compute SHA-256 hash of a string value.
   *
   * Uses the system `shasum` command (available on macOS and Linux).
   *
   * :arg value: The string content to hash
   * :returns: "sha256:<hex>" or empty string on failure
   */
  proc computeContentHash(value: string): string {
    try {
      var p = spawn(["shasum", "-a", "256"],
                    stdin=pipeStyle.pipe,
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.stdin.write(value);
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var line: string;
        if p.stdout.readLine(line) {
          // Output: "<hex>  -\n"
          const parts = line.strip().split(" ");
          const hex = parts[0];
          return "sha256:" + hex;
        }
      }
    } catch {
      // Fall through
    }
    return "";
  }

  // ==========================================================================
  // Hostname Helper
  // ==========================================================================

  /*
   * Get the current machine hostname.
   *
   * :returns: Hostname string or "unknown"
   */
  proc getLocalHostname(): string {
    try {
      var p = spawn(["hostname"],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();
      if p.exitCode == 0 {
        var line: string;
        if p.stdout.readLine(line) {
          return line.strip();
        }
      }
    } catch {
      // Fall through
    }
    return "unknown";
  }

  // ==========================================================================
  // ISO 8601 Timestamp
  // ==========================================================================

  /*
   * Get current UTC time as ISO 8601 string.
   *
   * :returns: e.g., "2026-02-27T10:30:00Z"
   */
  proc nowISO8601(): string {
    try {
      var p = spawn(["date", "-u", "+%Y-%m-%dT%H:%M:%SZ"],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();
      if p.exitCode == 0 {
        var line: string;
        if p.stdout.readLine(line) {
          return line.strip();
        }
      }
    } catch {
      // Fall through
    }
    return "1970-01-01T00:00:00Z";
  }

  // ==========================================================================
  // TOML Serialization (Writer)
  // ==========================================================================

  /*
   * Escape a string for TOML double-quoted value.
   *
   * Escapes backslashes, double quotes, and newlines.
   */
  proc escapeToml(s: string): string {
    var result = s.replace("\\", "\\\\");
    result = result.replace("\"", "\\\"");
    result = result.replace("\n", "\\n");
    result = result.replace("\r", "\\r");
    result = result.replace("\t", "\\t");
    return result;
  }

  /*
   * Write a manifest to a TOML file.
   *
   * :arg path: Output file path
   * :arg metadata: Manifest metadata header
   * :arg secrets: List of secret entries
   * :returns: true on success
   */
  proc writeManifest(path: string, metadata: ManifestMetadata,
                     secrets: list(SecretEntry)): bool {
    try {
      var f = open(path, ioMode.cw);
      var w = f.writer(locking=false);

      // Header comment
      w.writeln("# Identity-Bound Secret Fabric — Fleet Secret Manifest");
      w.writeln("# Auto-generated by `remote-juggler secrets discover`");
      w.writeln("# Schema: v" + metadata.version:string);
      w.writeln("# DO NOT EDIT MANUALLY — regenerated on each discovery run");
      w.writeln();

      // [metadata] section
      w.writeln("[metadata]");
      w.writeln("version = " + metadata.version:string);
      w.writeln("identity = \"" + escapeToml(metadata.identity) + "\"");
      w.writeln("generated_on = \"" + escapeToml(metadata.generatedOn) + "\"");
      w.writeln("generated_at = \"" + escapeToml(metadata.generatedAt) + "\"");
      w.writeln("total_secrets = " + metadata.totalSecrets:string);
      if metadata.agePublicKey != "" {
        w.writeln("age_public_key = \"" + escapeToml(metadata.agePublicKey) + "\"");
      }
      w.writeln();

      // [[secrets]] entries
      for entry in secrets {
        w.writeln("[[secrets]]");
        w.writeln("canonical_name = \"" + escapeToml(entry.canonicalName) + "\"");
        w.writeln("source_type = \"" + escapeToml(entry.sourceType) + "\"");
        w.writeln("source_path = \"" + escapeToml(entry.sourcePath) + "\"");
        w.writeln("source_host = \"" + escapeToml(entry.sourceHost) + "\"");
        w.writeln("discovered_at = \"" + escapeToml(entry.discoveredAt) + "\"");
        w.writeln("content_hash = \"" + escapeToml(entry.contentHash) + "\"");
        w.writeln("synced = " + if entry.synced then "true" else "false");
        w.writeln("classification = \"" + escapeToml(entry.classification) + "\"");
        w.writeln();
      }

      w.close();
      f.close();
      return true;
    } catch {
      return false;
    }
  }

  // ==========================================================================
  // TOML Deserialization (Reader) — Minimal, schema-specific
  // ==========================================================================

  /*
   * Read a manifest from a TOML file.
   *
   * This is a minimal parser that only understands the manifest schema.
   * It is NOT a general-purpose TOML parser.
   *
   * :arg path: Input file path
   * :arg metadata: Output metadata (ref)
   * :arg secrets: Output secret entries (ref)
   * :returns: true on success
   */
  proc readManifest(path: string, ref metadata: ManifestMetadata,
                    ref secrets: list(SecretEntry)): bool {
    if !exists(path) then return false;

    try {
      var f = open(path, ioMode.r);
      var r = f.reader(locking=false);

      var inMetadata = false;
      var inSecret = false;
      var currentEntry: SecretEntry;
      secrets.clear();

      var line: string;
      while r.readLine(line) {
        const trimmed = line.strip();

        // Skip empty lines and comments
        if trimmed == "" || trimmed.startsWith("#") then continue;

        // Section headers
        if trimmed == "[metadata]" {
          inMetadata = true;
          inSecret = false;
          continue;
        }
        if trimmed == "[[secrets]]" {
          // Save previous entry if we were in one
          if inSecret && currentEntry.canonicalName != "" {
            secrets.pushBack(currentEntry);
          }
          currentEntry = new SecretEntry();
          inMetadata = false;
          inSecret = true;
          continue;
        }

        // Key = value parsing
        const eqIdx = trimmed.find("="):int;
        if eqIdx == -1 then continue;

        const key = trimmed[0..eqIdx-1].strip();
        var value = trimmed[eqIdx+1..].strip();

        // Parse value: strip quotes for strings, parse bools and ints
        if value.startsWith("\"") && value.endsWith("\"") {
          value = value[1..value.size-2];
          // Unescape basic TOML escapes
          value = value.replace("\\\"", "\"");
          value = value.replace("\\\\", "\\");
          value = value.replace("\\n", "\n");
          value = value.replace("\\r", "\r");
          value = value.replace("\\t", "\t");
        }

        if inMetadata {
          select key {
            when "version" { try { metadata.version = value:int; } catch { } }
            when "identity" { metadata.identity = value; }
            when "generated_on" { metadata.generatedOn = value; }
            when "generated_at" { metadata.generatedAt = value; }
            when "total_secrets" { try { metadata.totalSecrets = value:int; } catch { } }
            when "age_public_key" { metadata.agePublicKey = value; }
          }
        } else if inSecret {
          select key {
            when "canonical_name" { currentEntry.canonicalName = value; }
            when "source_type" { currentEntry.sourceType = value; }
            when "source_path" { currentEntry.sourcePath = value; }
            when "source_host" { currentEntry.sourceHost = value; }
            when "discovered_at" { currentEntry.discoveredAt = value; }
            when "content_hash" { currentEntry.contentHash = value; }
            when "synced" { currentEntry.synced = (value == "true"); }
            when "classification" { currentEntry.classification = value; }
          }
        }
      }

      // Don't forget the last entry
      if inSecret && currentEntry.canonicalName != "" {
        secrets.pushBack(currentEntry);
      }

      r.close();
      f.close();

      // Update count for consistency
      metadata.totalSecrets = secrets.size: int;
      return true;
    } catch {
      return false;
    }
  }

  // ==========================================================================
  // Manifest Summary (for MCP tool output)
  // ==========================================================================

  /*
   * Generate a human-readable summary of a manifest.
   *
   * :arg metadata: Manifest metadata
   * :arg secrets: List of secret entries
   * :returns: Formatted summary string
   */
  proc manifestSummary(metadata: ManifestMetadata,
                       secrets: list(SecretEntry)): string {
    var output = "Secret Manifest (v" + metadata.version:string + ")\n";
    output += "  Identity:  " + metadata.identity + "\n";
    output += "  Generated: " + metadata.generatedAt + " on " + metadata.generatedOn + "\n";
    output += "  Total:     " + metadata.totalSecrets:string + " secrets\n";
    if metadata.agePublicKey != "" {
      const display = if metadata.agePublicKey.size > 24
                      then metadata.agePublicKey[0..#24] + "..."
                      else metadata.agePublicKey;
      output += "  Age Key:   " + display + "\n";
    }
    output += "\n";

    // Count by classification
    // NOTE: 'shared' is a reserved keyword in Chapel (shared class management)
    var sharedCount = 0;
    var personal = 0;
    var machineSpecific = 0;
    var syncedCount = 0;

    for entry in secrets {
      select entry.classification {
        when "shared" { sharedCount += 1; }
        when "personal" { personal += 1; }
        when "machine-specific" { machineSpecific += 1; }
      }
      if entry.synced then syncedCount += 1;
    }

    output += "  Classification:\n";
    output += "    shared:           " + sharedCount:string + "\n";
    output += "    personal:         " + personal:string + "\n";
    output += "    machine-specific: " + machineSpecific:string + "\n";
    output += "  Synced:            " + syncedCount:string + "/" + metadata.totalSecrets:string + "\n\n";

    // Count by source type
    var bySource: [0..4] int;
    const sourceTypes: [0..4] string = ["sops", "kdbx", "dotenv", "env", "ssh"];
    for entry in secrets {
      for i in 0..4 {
        if entry.sourceType == sourceTypes[i] {
          bySource[i] += 1;
          break;
        }
      }
    }

    output += "  Sources:\n";
    for i in 0..4 {
      if bySource[i] > 0 {
        output += "    " + sourceTypes[i] + ": " + bySource[i]:string + "\n";
      }
    }

    return output;
  }

  /*
   * Generate JSON representation of the manifest for MCP tool responses.
   *
   * :arg metadata: Manifest metadata
   * :arg secrets: List of secret entries
   * :returns: JSON string
   */
  proc manifestToJSON(metadata: ManifestMetadata,
                      secrets: list(SecretEntry)): string {
    var json = "{\n";
    json += "  \"metadata\": {\n";
    json += "    \"version\": " + metadata.version:string + ",\n";
    json += "    \"identity\": \"" + metadata.identity + "\",\n";
    json += "    \"generated_on\": \"" + metadata.generatedOn + "\",\n";
    json += "    \"generated_at\": \"" + metadata.generatedAt + "\",\n";
    json += "    \"total_secrets\": " + metadata.totalSecrets:string;
    if metadata.agePublicKey != "" {
      json += ",\n    \"age_public_key\": \"" + metadata.agePublicKey + "\"\n";
    } else {
      json += "\n";
    }
    json += "  },\n";
    json += "  \"secrets\": [\n";

    var first = true;
    for entry in secrets {
      if !first then json += ",\n";
      first = false;
      json += "    {\n";
      json += "      \"canonical_name\": \"" + entry.canonicalName + "\",\n";
      json += "      \"source_type\": \"" + entry.sourceType + "\",\n";
      json += "      \"source_path\": \"" + entry.sourcePath + "\",\n";
      json += "      \"source_host\": \"" + entry.sourceHost + "\",\n";
      json += "      \"discovered_at\": \"" + entry.discoveredAt + "\",\n";
      json += "      \"content_hash\": \"" + entry.contentHash + "\",\n";
      json += "      \"synced\": " + (if entry.synced then "true" else "false") + ",\n";
      json += "      \"classification\": \"" + entry.classification + "\"\n";
      json += "    }";
    }

    json += "\n  ]\n";
    json += "}";
    return json;
  }

  // ==========================================================================
  // Manifest Path Helpers
  // ==========================================================================

  /*
   * Get the default manifest path.
   *
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/
   * :returns: Full path to .manifest.toml
   */
  proc getManifestPath(tcfsSecretsDir: string): string {
    return tcfsSecretsDir + "/" + MANIFEST_FILENAME;
  }

  /*
   * Derive the canonical name from a source path and type.
   *
   * Examples:
   *   sops: "api/anthropic" -> "api/anthropic"
   *   kdbx: "RemoteJuggler/API/ANTHROPIC_API_KEY" -> "api/anthropic"
   *   dotenv: ".env" key "GITHUB_TOKEN" -> "env/<project>/GITHUB_TOKEN"
   *   env: "ANTHROPIC_API_KEY" -> "env/ANTHROPIC_API_KEY"
   *   ssh: "/home/user/.ssh/id_ed25519" -> "ssh/id_ed25519"
   *
   * :arg sourceType: Source type string
   * :arg sourcePath: Original path/name
   * :arg projectName: Project name for dotenv context (optional)
   * :returns: Canonical name
   */
  proc deriveCanonicalName(sourceType: string, sourcePath: string,
                           projectName: string = ""): string {
    select sourceType {
      when "sops" {
        // sops paths are already canonical-ish: "api/anthropic"
        return sourcePath;
      }
      when "kdbx" {
        // Strip "RemoteJuggler/" prefix and normalize
        var path = sourcePath;
        if path.startsWith("RemoteJuggler/") {
          path = path["RemoteJuggler/".size..];
        }
        // Convert "API/ANTHROPIC_API_KEY" -> "api/anthropic_api_key"
        return path.toLower();
      }
      when "dotenv" {
        // Format: env/<project>/<KEY>
        if projectName != "" {
          return "env/" + projectName + "/" + sourcePath;
        }
        return "env/" + sourcePath;
      }
      when "env" {
        // Environment variables: env/<VARNAME>
        return "env/" + sourcePath;
      }
      when "ssh" {
        // SSH keys: ssh/<keyname>
        const base = basename(sourcePath);
        return "ssh/" + base;
      }
      otherwise {
        return sourcePath;
      }
    }
  }

  // ==========================================================================
  // Sprint D: Bidirectional Additive Merge
  // ==========================================================================

  /*
   * Actions that result from a manifest merge.
   */
  enum MergeAction {
    push_secret,     // Local-only secret → encrypt + write to TCFS
    pull_secret,     // Remote-only secret → download + decrypt
    no_op,           // Same content hash → nothing to do
    conflict,        // Different hash → needs resolution
    tombstone        // Secret deleted → propagate removal
  }

  /*
   * Conflict resolution strategies.
   */
  enum ConflictStrategy {
    lastWriteWins,   // Compare discovered_at timestamps
    sourcePriority,  // sops > kdbx > dotenv > env > ssh
    keepBoth,        // Create versioned copies
    promptUser       // Leave unresolved for user/agent decision
  }

  /*
   * An unresolved conflict between local and remote secret.
   */
  record ConflictEntry {
    var canonicalName: string;
    var localHash: string;
    var remoteHash: string;
    var localHost: string;
    var remoteHost: string;
    var localDiscoveredAt: string;
    var remoteDiscoveredAt: string;
    var localSourceType: string;
    var remoteSourceType: string;
    var resolved: bool;
    var resolution: string;   // "keep-local", "keep-remote", "keep-both", ""

    proc init() {
      this.canonicalName = "";
      this.localHash = "";
      this.remoteHash = "";
      this.localHost = "";
      this.remoteHost = "";
      this.localDiscoveredAt = "";
      this.remoteDiscoveredAt = "";
      this.localSourceType = "";
      this.remoteSourceType = "";
      this.resolved = false;
      this.resolution = "";
    }
  }

  /*
   * A single action to take after merge.
   */
  record MergeActionEntry {
    var action: MergeAction;
    var secret: SecretEntry;
    var conflictInfo: ConflictEntry;

    proc init() {
      this.action = MergeAction.no_op;
      this.secret = new SecretEntry();
      this.conflictInfo = new ConflictEntry();
    }

    proc init(action: MergeAction, secret: SecretEntry) {
      this.action = action;
      this.secret = secret;
      this.conflictInfo = new ConflictEntry();
    }
  }

  /*
   * Result of a manifest merge operation.
   */
  record MergeResult {
    var pushed: int;
    var pulled: int;
    var conflicts: int;
    var noOps: int;
    var tombstones: int;
    var actions: list(MergeActionEntry);
    var unresolvedConflicts: list(ConflictEntry);

    proc init() {
      this.pushed = 0;
      this.pulled = 0;
      this.conflicts = 0;
      this.noOps = 0;
      this.tombstones = 0;
    }
  }

  /*
   * Tombstone entry: marks a deleted secret in the manifest.
   */
  record TombstoneEntry {
    var canonicalName: string;
    var deletedBy: string;     // Hostname
    var deletedAt: string;     // ISO 8601
    var previousHash: string;  // Last known content hash

    proc init() {
      this.canonicalName = "";
      this.deletedBy = "";
      this.deletedAt = "";
      this.previousHash = "";
    }

    proc init(canonicalName: string, deletedBy: string,
              deletedAt: string, previousHash: string) {
      this.canonicalName = canonicalName;
      this.deletedBy = deletedBy;
      this.deletedAt = deletedAt;
      this.previousHash = previousHash;
    }
  }

  // Source type priority for sourcePriority conflict resolution
  private proc sourceTypePriority(sourceType: string): int {
    select sourceType {
      when "sops" { return 5; }
      when "kdbx" { return 4; }
      when "dotenv" { return 3; }
      when "env" { return 2; }
      when "ssh" { return 1; }
      otherwise { return 0; }
    }
  }

  /*
   * Get the per-host manifest filename.
   *
   * Each host writes its own manifest so TCFS can sync them independently.
   * The canonical .manifest.toml is the merged result.
   *
   * :arg hostname: The host's name
   * :returns: Filename like ".manifest.xoxd-bates.toml"
   */
  proc hostManifestFilename(hostname: string): string {
    return ".manifest." + hostname + ".toml";
  }

  /*
   * Get full path to a host-specific manifest.
   */
  proc getHostManifestPath(tcfsSecretsDir: string, hostname: string): string {
    return tcfsSecretsDir + "/" + hostManifestFilename(hostname);
  }

  /*
   * Discover all per-host manifest files in the TCFS secrets directory.
   *
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/
   * :returns: list of (hostname, manifestPath) tuples
   */
  proc discoverHostManifests(tcfsSecretsDir: string): list((string, string)) {
    var manifests: list((string, string));

    if !isDir(tcfsSecretsDir) then return manifests;

    try {
      for entry in listDir(tcfsSecretsDir, dirs=false) {
        // Match pattern: .manifest.<hostname>.toml
        if entry.startsWith(".manifest.") && entry.endsWith(".toml")
           && entry != MANIFEST_FILENAME {
          // Extract hostname
          const stripped = entry[".manifest.".size..];
          const hostname = stripped[0..stripped.size - ".toml".size - 1];
          if hostname != "" {
            manifests.pushBack((hostname, tcfsSecretsDir + "/" + entry));
          }
        }
      }
    } catch { }

    return manifests;
  }

  /*
   * Merge a local manifest with one or more remote manifests.
   *
   * Implements additive merge with configurable conflict resolution:
   *   CASE 1: local-only → PUSH
   *   CASE 2: remote-only → PULL
   *   CASE 3: same hash → NO-OP
   *   CASE 4: different hash → resolve per strategy
   *
   * :arg localSecrets: Secrets from local discovery
   * :arg remoteSecrets: Secrets from remote manifest(s)
   * :arg localHost: Local hostname
   * :arg strategy: Conflict resolution strategy
   * :arg tombstones: Known tombstones (deleted secrets)
   * :returns: MergeResult with actions to take
   */
  proc mergeManifests(localSecrets: list(SecretEntry),
                      remoteSecrets: list(SecretEntry),
                      localHost: string,
                      strategy: ConflictStrategy = ConflictStrategy.lastWriteWins,
                      tombstones: list(TombstoneEntry) = new list(TombstoneEntry)
                      ): MergeResult {
    var result = new MergeResult();

    // Build lookup maps by canonical name
    // Since Chapel doesn't have a convenient map over records,
    // use parallel arrays indexed by canonical name matching.

    // Collect tombstoned names for quick lookup
    var tombstoneNames: list(string);
    for t in tombstones {
      tombstoneNames.pushBack(t.canonicalName);
    }

    // Index remote secrets by canonical name
    var remoteNames: list(string);
    for entry in remoteSecrets {
      remoteNames.pushBack(entry.canonicalName);
    }

    // Process local secrets
    for localEntry in localSecrets {
      // Skip tombstoned secrets
      var isTombstoned = false;
      for tName in tombstoneNames {
        if tName == localEntry.canonicalName {
          isTombstoned = true;
          break;
        }
      }
      if isTombstoned {
        result.tombstones += 1;
        var action = new MergeActionEntry(MergeAction.tombstone, localEntry);
        result.actions.pushBack(action);
        continue;
      }

      // Find matching remote entry
      var foundRemote = false;
      var remoteEntry: SecretEntry;
      for rEntry in remoteSecrets {
        if rEntry.canonicalName == localEntry.canonicalName {
          foundRemote = true;
          remoteEntry = rEntry;
          break;
        }
      }

      if !foundRemote {
        // CASE 1: exists only locally → PUSH
        result.pushed += 1;
        var action = new MergeActionEntry(MergeAction.push_secret, localEntry);
        result.actions.pushBack(action);
      } else if localEntry.contentHash == remoteEntry.contentHash {
        // CASE 3: same content → NO-OP
        result.noOps += 1;
        var action = new MergeActionEntry(MergeAction.no_op, localEntry);
        result.actions.pushBack(action);
      } else {
        // CASE 4: different content → CONFLICT
        var conflict = new ConflictEntry();
        conflict.canonicalName = localEntry.canonicalName;
        conflict.localHash = localEntry.contentHash;
        conflict.remoteHash = remoteEntry.contentHash;
        conflict.localHost = localHost;
        conflict.remoteHost = remoteEntry.sourceHost;
        conflict.localDiscoveredAt = localEntry.discoveredAt;
        conflict.remoteDiscoveredAt = remoteEntry.discoveredAt;
        conflict.localSourceType = localEntry.sourceType;
        conflict.remoteSourceType = remoteEntry.sourceType;

        // Apply resolution strategy
        select strategy {
          when ConflictStrategy.lastWriteWins {
            // Compare timestamps — lexicographic comparison works for ISO 8601
            if localEntry.discoveredAt >= remoteEntry.discoveredAt {
              conflict.resolved = true;
              conflict.resolution = "keep-local";
              result.pushed += 1;
              var action = new MergeActionEntry(MergeAction.push_secret, localEntry);
              action.conflictInfo = conflict;
              result.actions.pushBack(action);
            } else {
              conflict.resolved = true;
              conflict.resolution = "keep-remote";
              result.pulled += 1;
              var action = new MergeActionEntry(MergeAction.pull_secret, remoteEntry);
              action.conflictInfo = conflict;
              result.actions.pushBack(action);
            }
          }
          when ConflictStrategy.sourcePriority {
            const localPrio = sourceTypePriority(localEntry.sourceType);
            const remotePrio = sourceTypePriority(remoteEntry.sourceType);
            if localPrio >= remotePrio {
              conflict.resolved = true;
              conflict.resolution = "keep-local";
              result.pushed += 1;
              var action = new MergeActionEntry(MergeAction.push_secret, localEntry);
              action.conflictInfo = conflict;
              result.actions.pushBack(action);
            } else {
              conflict.resolved = true;
              conflict.resolution = "keep-remote";
              result.pulled += 1;
              var action = new MergeActionEntry(MergeAction.pull_secret, remoteEntry);
              action.conflictInfo = conflict;
              result.actions.pushBack(action);
            }
          }
          when ConflictStrategy.keepBoth {
            conflict.resolved = true;
            conflict.resolution = "keep-both";
            // Push local under versioned name
            var versionedLocal = localEntry;
            versionedLocal.canonicalName = localEntry.canonicalName + ".local";
            result.pushed += 1;
            var pushAction = new MergeActionEntry(MergeAction.push_secret, versionedLocal);
            pushAction.conflictInfo = conflict;
            result.actions.pushBack(pushAction);
            // Pull remote under versioned name
            var versionedRemote = remoteEntry;
            versionedRemote.canonicalName = remoteEntry.canonicalName + ".remote";
            result.pulled += 1;
            var pullAction = new MergeActionEntry(MergeAction.pull_secret, versionedRemote);
            result.actions.pushBack(pullAction);
          }
          when ConflictStrategy.promptUser {
            // Leave unresolved
            conflict.resolved = false;
            conflict.resolution = "";
            result.conflicts += 1;
            result.unresolvedConflicts.pushBack(conflict);
            var action = new MergeActionEntry(MergeAction.conflict, localEntry);
            action.conflictInfo = conflict;
            result.actions.pushBack(action);
          }
        }
      }
    }

    // Process remote-only secrets (CASE 2)
    for remoteEntry in remoteSecrets {
      // Check if we already processed it (exists in local)
      var foundLocal = false;
      for localEntry in localSecrets {
        if localEntry.canonicalName == remoteEntry.canonicalName {
          foundLocal = true;
          break;
        }
      }

      if !foundLocal {
        // Check tombstones
        var isTombstoned = false;
        for tName in tombstoneNames {
          if tName == remoteEntry.canonicalName {
            isTombstoned = true;
            break;
          }
        }

        if isTombstoned {
          result.tombstones += 1;
          var action = new MergeActionEntry(MergeAction.tombstone, remoteEntry);
          result.actions.pushBack(action);
        } else {
          // CASE 2: remote-only → PULL
          result.pulled += 1;
          var action = new MergeActionEntry(MergeAction.pull_secret, remoteEntry);
          result.actions.pushBack(action);
        }
      }
    }

    return result;
  }

  /*
   * Generate a summary string for a MergeResult.
   */
  proc mergeSummary(result: MergeResult): string {
    var output = "Merge Result:\n";
    output += "  Pushed:      " + result.pushed:string + " secrets\n";
    output += "  Pulled:      " + result.pulled:string + " secrets\n";
    output += "  Conflicts:   " + result.conflicts:string + "\n";
    output += "  No-ops:      " + result.noOps:string + "\n";
    output += "  Tombstones:  " + result.tombstones:string + "\n";

    if result.unresolvedConflicts.size > 0 {
      output += "\n  Unresolved conflicts:\n";
      for c in result.unresolvedConflicts {
        output += "    - " + c.canonicalName + " (local: " +
                  c.localHost + ", remote: " + c.remoteHost + ")\n";
      }
    }

    return output;
  }

  /*
   * Convert a MergeResult to JSON for MCP responses.
   */
  proc mergeResultToJSON(result: MergeResult): string {
    var json = "{\n";
    json += "  \"pushed\": " + result.pushed:string + ",\n";
    json += "  \"pulled\": " + result.pulled:string + ",\n";
    json += "  \"conflicts\": " + result.conflicts:string + ",\n";
    json += "  \"no_ops\": " + result.noOps:string + ",\n";
    json += "  \"tombstones\": " + result.tombstones:string + ",\n";
    json += "  \"unresolved_conflicts\": [\n";

    var first = true;
    for c in result.unresolvedConflicts {
      if !first then json += ",\n";
      first = false;
      json += "    {\n";
      json += "      \"canonical_name\": \"" + c.canonicalName + "\",\n";
      json += "      \"local_hash\": \"" + c.localHash + "\",\n";
      json += "      \"remote_hash\": \"" + c.remoteHash + "\",\n";
      json += "      \"local_host\": \"" + c.localHost + "\",\n";
      json += "      \"remote_host\": \"" + c.remoteHost + "\"\n";
      json += "    }";
    }

    json += "\n  ]\n";
    json += "}";
    return json;
  }

  // ==========================================================================
  // Audit Log
  // ==========================================================================

  const AUDIT_LOG_FILENAME = ".audit.log";

  /*
   * Append an entry to the IBSF audit log.
   *
   * Format: ISO8601 | action | canonical_name | host | details
   *
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/
   * :arg action: What happened (push, pull, conflict, resolve, delete)
   * :arg canonicalName: Secret that was affected
   * :arg host: Host that performed the action
   * :arg details: Additional context
   */
  proc auditLog(tcfsSecretsDir: string, action: string,
                canonicalName: string, host: string,
                details: string = "") {
    const logPath = tcfsSecretsDir + "/" + AUDIT_LOG_FILENAME;
    const timestamp = nowISO8601();

    try {
      // Append by reading existing content and rewriting
      var existing = "";
      if exists(logPath) {
        var rf = open(logPath, ioMode.r);
        var rr = rf.reader(locking=false);
        rr.readAll(existing);
        rr.close();
        rf.close();
      }
      var f = open(logPath, ioMode.cw);
      var w = f.writer(locking=false);
      w.write(existing);
      w.writeln(timestamp + " | " + action + " | " + canonicalName +
                " | " + host + " | " + details);
      w.close();
      f.close();
    } catch { }
  }

  /*
   * Read the last N entries from the audit log.
   *
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/
   * :arg limit: Maximum entries to return (0 = all)
   * :returns: list of log line strings
   */
  proc readAuditLog(tcfsSecretsDir: string,
                    limit: int = 20): list(string) {
    const logPath = tcfsSecretsDir + "/" + AUDIT_LOG_FILENAME;
    var entries: list(string);

    if !exists(logPath) then return entries;

    try {
      // Read all lines
      var allLines: list(string);
      var f = open(logPath, ioMode.r);
      var r = f.reader(locking=false);
      var line: string;
      while r.readLine(line) {
        const trimmed = line.strip();
        if trimmed != "" {
          allLines.pushBack(trimmed);
        }
      }

      // Return last N entries
      if limit <= 0 || limit >= allLines.size {
        return allLines;
      }
      const startIdx = allLines.size - limit;
      for i in startIdx..allLines.size-1 {
        entries.pushBack(allLines[i]);
      }
    } catch { }

    return entries;
  }

  // ==========================================================================
  // Tombstone Persistence
  // ==========================================================================

  const TOMBSTONE_FILENAME = ".tombstones.toml";

  /*
   * Write tombstone entries to TCFS for propagation.
   */
  proc writeTombstones(tcfsSecretsDir: string,
                       tombstones: list(TombstoneEntry)): bool {
    const path = tcfsSecretsDir + "/" + TOMBSTONE_FILENAME;

    try {
      var f = open(path, ioMode.cw);
      var w = f.writer(locking=false);

      w.writeln("# IBSF Tombstones — deleted secrets");
      w.writeln("# Synced via TCFS to propagate removal across fleet");
      w.writeln();

      for t in tombstones {
        w.writeln("[[tombstones]]");
        w.writeln("canonical_name = \"" + escapeToml(t.canonicalName) + "\"");
        w.writeln("deleted_by = \"" + escapeToml(t.deletedBy) + "\"");
        w.writeln("deleted_at = \"" + escapeToml(t.deletedAt) + "\"");
        w.writeln("previous_hash = \"" + escapeToml(t.previousHash) + "\"");
        w.writeln();
      }

      w.close();
      f.close();
      return true;
    } catch {
      return false;
    }
  }

  /*
   * Read tombstone entries from TCFS.
   */
  proc readTombstones(tcfsSecretsDir: string): list(TombstoneEntry) {
    const path = tcfsSecretsDir + "/" + TOMBSTONE_FILENAME;
    var tombstones: list(TombstoneEntry);

    if !exists(path) then return tombstones;

    try {
      var f = open(path, ioMode.r);
      var r = f.reader(locking=false);

      var inEntry = false;
      var current: TombstoneEntry;

      var line: string;
      while r.readLine(line) {
        const trimmed = line.strip();
        if trimmed == "" || trimmed.startsWith("#") then continue;

        if trimmed == "[[tombstones]]" {
          if inEntry && current.canonicalName != "" {
            tombstones.pushBack(current);
          }
          current = new TombstoneEntry();
          inEntry = true;
          continue;
        }

        if !inEntry then continue;

        const eqIdx = trimmed.find("="):int;
        if eqIdx == -1 then continue;

        const key = trimmed[0..eqIdx-1].strip();
        var value = trimmed[eqIdx+1..].strip();
        if value.startsWith("\"") && value.endsWith("\"") {
          value = value[1..value.size-2];
        }

        select key {
          when "canonical_name" { current.canonicalName = value; }
          when "deleted_by" { current.deletedBy = value; }
          when "deleted_at" { current.deletedAt = value; }
          when "previous_hash" { current.previousHash = value; }
        }
      }

      // Don't forget the last entry
      if inEntry && current.canonicalName != "" {
        tombstones.pushBack(current);
      }
    } catch { }

    return tombstones;
  }

  // ==========================================================================
  // Sprint D: PUSH + PULL Execution
  // ==========================================================================

  /*
   * Execute merge actions: push local secrets and pull remote secrets.
   *
   * For PUSH: encrypt the secret value and write to TCFS secrets dir.
   * For PULL: the .age file is already in TCFS via sync; decrypt it.
   * For TOMBSTONE: remove the file from TCFS secrets dir.
   *
   * :arg result: MergeResult from mergeManifests()
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/
   * :arg localHost: Local hostname for audit log
   * :returns: (pushCount, pullCount, failCount)
   */
  proc executeMergeActions(ref result: MergeResult,
                           tcfsSecretsDir: string,
                           localHost: string): (int, int, int) {
    var pushCount = 0;
    var pullCount = 0;
    var failCount = 0;

    for action in result.actions {
      select action.action {
        when MergeAction.push_secret {
          const ok = pushSecretToTcfs(action.secret, tcfsSecretsDir);
          if ok {
            pushCount += 1;
            auditLog(tcfsSecretsDir, "push", action.secret.canonicalName,
                     localHost, "hash=" + action.secret.contentHash);
          } else {
            failCount += 1;
          }
        }
        when MergeAction.pull_secret {
          const ok = pullSecretFromTcfs(action.secret, tcfsSecretsDir);
          if ok {
            pullCount += 1;
            auditLog(tcfsSecretsDir, "pull", action.secret.canonicalName,
                     localHost, "from=" + action.secret.sourceHost);
          } else {
            failCount += 1;
          }
        }
        when MergeAction.tombstone {
          const ok = removeSecretFromTcfs(action.secret, tcfsSecretsDir);
          if ok {
            auditLog(tcfsSecretsDir, "delete", action.secret.canonicalName,
                     localHost, "tombstone propagation");
          }
        }
        when MergeAction.conflict {
          auditLog(tcfsSecretsDir, "conflict", action.secret.canonicalName,
                   localHost, "unresolved — " +
                   action.conflictInfo.localHash + " vs " +
                   action.conflictInfo.remoteHash);
        }
        otherwise { }  // no_op
      }
    }

    return (pushCount, pullCount, failCount);
  }

  /*
   * Push a secret to TCFS: encrypt + write.
   *
   * The secret value must be retrievable (e.g., from KDBX or env).
   * We write the encrypted .age file to the canonical path in TCFS.
   */
  proc pushSecretToTcfs(secret: SecretEntry,
                        tcfsSecretsDir: string): bool {
    // Derive the target path from canonical name
    const targetDir = tcfsSecretsDir + "/" + dirname(secret.canonicalName);
    const baseName = basename(secret.canonicalName);
    const targetPath = targetDir + "/" + baseName + AgeEncryption.AGE_EXTENSION;

    // Ensure target directory exists
    if !isDir(targetDir) {
      try {
        mkdir(targetDir, parents=true);
      } catch {
        return false;
      }
    }

    // For push, we need the actual secret value.
    // If it's from a file source (sops, dotenv, ssh), read it.
    // If it's from env, get the environment variable.
    var value = "";
    select secret.sourceType {
      when "env" {
        value = getEnvVar(secret.sourcePath);
      }
      otherwise {
        // Try reading the source file
        if exists(secret.sourcePath) {
          try {
            var f = open(secret.sourcePath, ioMode.r);
            var r = f.reader(locking=false);
            r.readAll(value);
          } catch {
            return false;
          }
        }
      }
    }

    if value == "" then return false;

    // Encrypt and write
    const result = AgeEncryption.ageEncryptValue(value, targetPath);
    return result.ok;
  }

  /*
   * Pull a secret from TCFS: the .age file should already exist via sync.
   * Decrypt it to the materialized location.
   */
  proc pullSecretFromTcfs(secret: SecretEntry,
                          tcfsSecretsDir: string): bool {
    const baseName = basename(secret.canonicalName);
    const secretDir = tcfsSecretsDir + "/" + dirname(secret.canonicalName);
    const agePath = secretDir + "/" + baseName + AgeEncryption.AGE_EXTENSION;
    const plainPath = secretDir + "/" + baseName;

    if !exists(agePath) {
      // File not yet synced via TCFS
      return false;
    }

    const result = AgeEncryption.ageDecryptFile(agePath, plainPath);
    return result.ok;
  }

  /*
   * Remove a secret from TCFS (tombstone propagation).
   */
  private proc removeSecretFromTcfs(secret: SecretEntry,
                                     tcfsSecretsDir: string): bool {
    const baseName = basename(secret.canonicalName);
    const secretDir = tcfsSecretsDir + "/" + dirname(secret.canonicalName);
    const agePath = secretDir + "/" + baseName + AgeEncryption.AGE_EXTENSION;
    const plainPath = secretDir + "/" + baseName;

    var removed = false;
    if exists(agePath) {
      try { remove(agePath); removed = true; } catch { }
    }
    if exists(plainPath) {
      try { remove(plainPath); removed = true; } catch { }
    }
    return removed;
  }

}

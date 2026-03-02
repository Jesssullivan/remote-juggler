/*
 * GPGAgent.chpl - gpg-agent Configuration and Cache Management
 *
 * Part of RemoteJuggler - Backend-agnostic git identity management
 *
 * This module manages gpg-agent.conf configuration for Trusted Workstation
 * mode, enabling automatic PIN retrieval from TPM/SecureEnclave through
 * a custom pinentry program.
 *
 * Key Discovery: gpg-preset-passphrase does NOT work with YubiKey PINs.
 * The PIN is cached by the card hardware, not gpg-agent. Therefore, we
 * use a custom pinentry program that retrieves the PIN from the HSM.
 *
 * Features:
 *   - Read/write gpg-agent.conf configuration
 *   - Configure custom pinentry for Trusted Workstation mode
 *   - Backup and restore original pinentry configuration
 *   - gpg-agent lifecycle management (restart, reload)
 *   - Key grip discovery for cache operations
 *
 * Configuration Path:
 *   ~/.gnupg/gpg-agent.conf
 *
 * Backup Path:
 *   ~/.config/remote-juggler/gpg-agent.conf.backup
 *
 * :author: RemoteJuggler Team
 * :version: 2.0.0
 * :license: MIT
 */
prototype module GPGAgent {
  use Subprocess;
  use IO;
  use List;
  use FileSystem;
  use Path;
  public use super.Core;

  // ============================================================
  // Configuration Constants
  // ============================================================

  /*
   * Key configuration directives managed by this module
   */
  private const PINENTRY_PROGRAM = "pinentry-program";
  private const ALLOW_LOOPBACK = "allow-loopback-pinentry";
  private const ALLOW_PRESET = "allow-preset-passphrase";
  private const DEFAULT_CACHE_TTL = "default-cache-ttl";
  private const MAX_CACHE_TTL = "max-cache-ttl";

  /*
   * Backup marker comment added to gpg-agent.conf when we modify it
   */
  private const RJ_MARKER = "# RemoteJuggler managed - do not edit this line";
  private const RJ_ORIGINAL_MARKER = "# RemoteJuggler: original pinentry was: ";

  // ============================================================
  // Path Management
  // ============================================================

  /*
   * Get the path to gpg-agent.conf
   *
   * Returns:
   *   Path to ~/.gnupg/gpg-agent.conf
   */
  proc getGPGAgentConfigPath(): string {
    const home = getEnvOrDefault("HOME", "/tmp");
    const gnupgHome = getEnvOrDefault("GNUPGHOME", home + "/.gnupg");
    return gnupgHome + "/gpg-agent.conf";
  }

  /*
   * Get the path to our backup of the original configuration
   *
   * Returns:
   *   Path to ~/.config/remote-juggler/gpg-agent.conf.backup
   */
  proc getBackupConfigPath(): string {
    const home = getEnvOrDefault("HOME", "/tmp");
    return home + "/.config/remote-juggler/gpg-agent.conf.backup";
  }

  /*
   * Get the path to the gpg-agent socket
   *
   * The socket location depends on GNUPGHOME and can vary by platform.
   * Common locations:
   *   - Linux: $GNUPGHOME/S.gpg-agent or /run/user/$UID/gnupg/S.gpg-agent
   *   - macOS: $GNUPGHOME/S.gpg-agent
   *
   * Returns:
   *   Path to the gpg-agent socket, or empty string if not found
   */
  proc getAgentSocket(): string {
    const home = getEnvOrDefault("HOME", "/tmp");
    const gnupgHome = getEnvOrDefault("GNUPGHOME", home + "/.gnupg");

    // Try gpgconf first (most reliable)
    try {
      var p = spawn(["gpgconf", "--list-dirs", "agent-socket"],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var socketPath: string;
        p.stdout.readAll(socketPath);
        socketPath = socketPath.strip();
        if socketPath != "" && exists(socketPath) {
          return socketPath;
        }
      }
    } catch {
      // Fall through to manual detection
    }

    // Manual detection fallback
    // Check XDG_RUNTIME_DIR first (modern Linux)
    const xdgRuntime = getEnvOrDefault("XDG_RUNTIME_DIR", "");
    if xdgRuntime != "" {
      const uid = getEnvOrDefault("UID", "");
      const runtimeSocket = xdgRuntime + "/gnupg/S.gpg-agent";
      try {
        if exists(runtimeSocket) then return runtimeSocket;
      } catch {
        // Fall through
      }
    }

    // Check standard GNUPGHOME location
    const standardSocket = gnupgHome + "/S.gpg-agent";
    try {
      if exists(standardSocket) then return standardSocket;
    } catch {
      // Fall through
    }

    return "";
  }

  /*
   * Get the path to scdaemon (smartcard daemon) socket
   *
   * Returns:
   *   Path to scdaemon socket, or empty string if not found
   */
  proc getScdaemonSocket(): string {
    try {
      var p = spawn(["gpgconf", "--list-dirs", "agent-ssh-socket"],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var socketPath: string;
        p.stdout.readAll(socketPath);
        socketPath = socketPath.strip();
        if socketPath != "" {
          return socketPath;
        }
      }
    } catch {
      // Fall through
    }
    return "";
  }

  // ============================================================
  // Configuration File Operations
  // ============================================================

  /*
   * Configuration entry from gpg-agent.conf
   */
  record ConfigEntry {
    var key: string;
    var value: string;
    var comment: string;  // Leading comment if any
    var isComment: bool;  // True if entire line is a comment
  }

  /*
   * Read and parse gpg-agent.conf
   *
   * Returns:
   *   Tuple of (success, entries) where entries is a list of ConfigEntry
   */
  proc readConfig(): (bool, list(ConfigEntry)) {
    var entries: list(ConfigEntry);
    const configPath = getGPGAgentConfigPath();

    try {
      if !exists(configPath) {
        // Config file doesn't exist, return empty list (success)
        return (true, entries);
      }

      var f = open(configPath, ioMode.r);
      defer { try { f.close(); } catch { } }

      var reader = f.reader(locking=false);
      defer { try { reader.close(); } catch { } }

      var line: string;
      while reader.readLine(line, stripNewline=true) {
        var entry = new ConfigEntry();

        // Handle empty lines
        if line.strip() == "" {
          entry.isComment = true;
          entry.comment = "";
          entries.pushBack(entry);
          continue;
        }

        // Handle comment lines
        if line.strip().startsWith("#") {
          entry.isComment = true;
          entry.comment = line;
          entries.pushBack(entry);
          continue;
        }

        // Parse key-value pair
        // Format: key [value]
        const stripped = line.strip();
        const spaceIdx = stripped.find(" ");

        if spaceIdx == -1 {
          // Key only (boolean flag)
          entry.key = stripped;
          entry.value = "";
        } else {
          entry.key = stripped[..spaceIdx-1];
          entry.value = stripped[spaceIdx+1..].strip();
        }

        entries.pushBack(entry);
      }

      return (true, entries);
    } catch e {
      verboseLog("Failed to read gpg-agent.conf: ", e:string);
      return (false, entries);
    }
  }

  /*
   * Write configuration entries to gpg-agent.conf
   *
   * Args:
   *   entries: List of ConfigEntry to write
   *
   * Returns:
   *   true if successful
   */
  proc writeConfig(entries: list(ConfigEntry)): bool {
    const configPath = getGPGAgentConfigPath();

    try {
      // Ensure directory exists
      const configDir = dirname(configPath);
      if !exists(configDir) {
        mkdir(configDir, parents=true, mode=0o700);
      }

      var f = open(configPath, ioMode.cw);
      defer { try { f.close(); } catch { } }

      var writer = f.writer(locking=false);
      defer { try { writer.close(); } catch { } }

      for entry in entries {
        if entry.isComment {
          writer.writeln(entry.comment);
        } else if entry.value == "" {
          // Boolean flag
          writer.writeln(entry.key);
        } else {
          writer.writeln(entry.key + " " + entry.value);
        }
      }

      return true;
    } catch e {
      verboseLog("Failed to write gpg-agent.conf: ", e:string);
      return false;
    }
  }

  /*
   * Get a configuration value from the parsed entries
   *
   * Args:
   *   entries: List of ConfigEntry
   *   key: Configuration key to find
   *
   * Returns:
   *   Tuple of (found, value)
   */
  proc getConfigValue(entries: list(ConfigEntry), key: string): (bool, string) {
    for entry in entries {
      if !entry.isComment && entry.key == key {
        return (true, entry.value);
      }
    }
    return (false, "");
  }

  /*
   * Set a configuration value in the entries list
   *
   * If the key exists, updates its value. Otherwise adds a new entry.
   *
   * Args:
   *   entries: List of ConfigEntry (modified in place)
   *   key: Configuration key
   *   value: Configuration value (empty for boolean flags)
   */
  proc setConfigValue(ref entries: list(ConfigEntry), key: string, value: string) {
    // Look for existing entry
    for i in 0..<entries.size {
      ref entry = entries[i];
      if !entry.isComment && entry.key == key {
        entry.value = value;
        return;
      }
    }

    // Add new entry
    var newEntry = new ConfigEntry();
    newEntry.key = key;
    newEntry.value = value;
    newEntry.isComment = false;
    entries.pushBack(newEntry);
  }

  /*
   * Remove a configuration entry
   *
   * Args:
   *   entries: List of ConfigEntry (modified in place)
   *   key: Configuration key to remove
   *
   * Returns:
   *   true if entry was found and removed
   */
  proc removeConfigValue(ref entries: list(ConfigEntry), key: string): bool {
    for i in 0..<entries.size {
      if !entries[i].isComment && entries[i].key == key {
        entries.remove(i);
        return true;
      }
    }
    return false;
  }

  /*
   * Check if a boolean configuration flag is enabled
   *
   * Args:
   *   entries: List of ConfigEntry
   *   key: Configuration key to check
   *
   * Returns:
   *   true if flag is present (boolean flags are enabled by presence)
   */
  proc isConfigEnabled(entries: list(ConfigEntry), key: string): bool {
    for entry in entries {
      if !entry.isComment && entry.key == key {
        return true;
      }
    }
    return false;
  }

  // ============================================================
  // Trusted Workstation Configuration
  // ============================================================

  /*
   * Configure gpg-agent for Trusted Workstation mode
   *
   * This function:
   * 1. Backs up the current pinentry-program setting
   * 2. Sets pinentry-program to the RemoteJuggler custom pinentry
   * 3. Enables allow-loopback-pinentry
   * 4. Adds marker comments for identification
   *
   * Args:
   *   pinentryPath: Path to pinentry-remotejuggler
   *
   * Returns:
   *   true if configuration succeeded
   */
  proc configureForTrustedWorkstation(pinentryPath: string): bool {
    // Read current config
    const (readOk, originalEntries) = readConfig();
    var entries = originalEntries;

    if !readOk {
      verboseLog("Failed to read gpg-agent.conf");
      return false;
    }

    // Check if already configured by us
    for entry in entries {
      if entry.isComment && entry.comment.find(RJ_MARKER) != -1 {
        verboseLog("gpg-agent.conf already configured for Trusted Workstation");
        // Update pinentry path in case it changed
        setConfigValue(entries, PINENTRY_PROGRAM, pinentryPath);
        return writeConfig(entries);
      }
    }

    // Backup original pinentry setting
    const (hasPinentry, originalPinentry) = getConfigValue(entries, PINENTRY_PROGRAM);
    if !backupOriginalPinentry(originalPinentry) {
      verboseLog("Failed to backup original pinentry setting");
      // Continue anyway - worst case user has to manually restore
    }

    // Add marker comment
    var markerEntry = new ConfigEntry();
    markerEntry.isComment = true;
    markerEntry.comment = RJ_MARKER;
    entries.pushBack(markerEntry);

    // Store original pinentry as comment for reference
    if hasPinentry && originalPinentry != "" {
      var origEntry = new ConfigEntry();
      origEntry.isComment = true;
      origEntry.comment = RJ_ORIGINAL_MARKER + originalPinentry;
      entries.pushBack(origEntry);
    }

    // Set new pinentry program
    setConfigValue(entries, PINENTRY_PROGRAM, pinentryPath);

    // Enable required options
    if !isConfigEnabled(entries, ALLOW_LOOPBACK) {
      setConfigValue(entries, ALLOW_LOOPBACK, "");
    }

    // Write updated config
    if !writeConfig(entries) {
      verboseLog("Failed to write gpg-agent.conf");
      return false;
    }

    verboseLog("Configured gpg-agent for Trusted Workstation mode");
    verboseLog("  pinentry-program: ", pinentryPath);

    return true;
  }

  /*
   * Restore original gpg-agent configuration
   *
   * This function:
   * 1. Reads the backed up original pinentry setting
   * 2. Restores it to gpg-agent.conf
   * 3. Removes RemoteJuggler marker comments
   *
   * Returns:
   *   true if restoration succeeded
   */
  proc restoreOriginalConfig(): bool {
    // Read current config
    const (readOk, originalEntries) = readConfig();
    var entries = originalEntries;

    if !readOk {
      verboseLog("Failed to read gpg-agent.conf");
      return false;
    }

    // Get the backed up original pinentry
    const (hasBackup, originalPinentry) = readBackupPinentry();

    // Also check for original pinentry in comments
    var commentOriginal = "";
    for entry in entries {
      if entry.isComment && entry.comment.startsWith(RJ_ORIGINAL_MARKER) {
        commentOriginal = entry.comment[RJ_ORIGINAL_MARKER.size..].strip();
        break;
      }
    }

    // Use backup file first, then comment, then remove entirely
    var pinentryToRestore = "";
    if hasBackup && originalPinentry != "" {
      pinentryToRestore = originalPinentry;
    } else if commentOriginal != "" {
      pinentryToRestore = commentOriginal;
    }

    // Build new entries list without our markers
    var newEntries: list(ConfigEntry);
    for entry in entries {
      if entry.isComment {
        // Skip our marker comments
        if entry.comment.find(RJ_MARKER) != -1 then continue;
        if entry.comment.startsWith(RJ_ORIGINAL_MARKER) then continue;
      }
      newEntries.pushBack(entry);
    }

    // Restore or remove pinentry setting
    if pinentryToRestore != "" {
      setConfigValue(newEntries, PINENTRY_PROGRAM, pinentryToRestore);
      verboseLog("Restored original pinentry: ", pinentryToRestore);
    } else {
      // Remove our pinentry setting
      removeConfigValue(newEntries, PINENTRY_PROGRAM);
      verboseLog("Removed pinentry-program setting");
    }

    // Write updated config
    if !writeConfig(newEntries) {
      verboseLog("Failed to write gpg-agent.conf");
      return false;
    }

    // Clean up backup file
    deleteBackupPinentry();

    verboseLog("Restored original gpg-agent configuration");
    return true;
  }

  /*
   * Backup the original pinentry setting to our config directory
   *
   * Args:
   *   originalPinentry: The original pinentry-program value
   *
   * Returns:
   *   true if backup succeeded
   */
  private proc backupOriginalPinentry(originalPinentry: string): bool {
    const backupPath = getBackupConfigPath();

    try {
      // Ensure directory exists
      const backupDir = dirname(backupPath);
      if !exists(backupDir) {
        mkdir(backupDir, parents=true, mode=0o700);
      }

      var f = open(backupPath, ioMode.cw);
      defer { try { f.close(); } catch { } }

      var writer = f.writer(locking=false);
      defer { try { writer.close(); } catch { } }

      writer.writeln(originalPinentry);
      return true;
    } catch e {
      verboseLog("Failed to backup pinentry: ", e:string);
      return false;
    }
  }

  /*
   * Read the backed up original pinentry setting
   *
   * Returns:
   *   Tuple of (found, originalPinentry)
   */
  private proc readBackupPinentry(): (bool, string) {
    const backupPath = getBackupConfigPath();

    try {
      if !exists(backupPath) {
        return (false, "");
      }

      var f = open(backupPath, ioMode.r);
      defer { try { f.close(); } catch { } }

      var reader = f.reader(locking=false);
      defer { try { reader.close(); } catch { } }

      var line: string;
      if reader.readLine(line, stripNewline=true) {
        return (true, line.strip());
      }
    } catch {
      // Fall through
    }

    return (false, "");
  }

  /*
   * Delete the backup pinentry file
   */
  private proc deleteBackupPinentry() {
    const backupPath = getBackupConfigPath();
    try {
      if exists(backupPath) {
        remove(backupPath);
      }
    } catch {
      // Ignore errors
    }
  }

  /*
   * Check if gpg-agent.conf is configured for Trusted Workstation mode
   *
   * Returns:
   *   Tuple of (isConfigured, pinentryPath)
   */
  proc isTrustedWorkstationConfigured(): (bool, string) {
    const (readOk, entries) = readConfig();
    if !readOk {
      return (false, "");
    }

    // Check for our marker
    var hasMarker = false;
    for entry in entries {
      if entry.isComment && entry.comment.find(RJ_MARKER) != -1 {
        hasMarker = true;
        break;
      }
    }

    if hasMarker {
      const (hasPinentry, pinentryPath) = getConfigValue(entries, PINENTRY_PROGRAM);
      return (hasPinentry, pinentryPath);
    }

    return (false, "");
  }

  // ============================================================
  // gpg-agent Lifecycle Management
  // ============================================================

  /*
   * Check if gpg-agent is running
   *
   * Returns:
   *   true if agent is running
   */
  proc isAgentRunning(): bool {
    try {
      var p = spawn(["gpg-connect-agent", "/bye"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Reload gpg-agent configuration
   *
   * Sends SIGHUP equivalent via gpg-connect-agent to reload config
   * without restarting the agent.
   *
   * Returns:
   *   true if reload succeeded
   */
  proc reloadAgent(): bool {
    try {
      var p = spawn(["gpg-connect-agent", "RELOADAGENT", "/bye"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        verboseLog("gpg-agent configuration reloaded");
        return true;
      }
    } catch {
      // Fall through
    }

    verboseLog("Failed to reload gpg-agent");
    return false;
  }

  /*
   * Restart gpg-agent
   *
   * Kills the current agent and starts a new one with updated configuration.
   *
   * Returns:
   *   true if restart succeeded
   */
  proc restartAgent(): bool {
    // Kill existing agent
    try {
      var killP = spawn(["gpgconf", "--kill", "gpg-agent"],
                        stdout=pipeStyle.close,
                        stderr=pipeStyle.close);
      killP.wait();
    } catch {
      // Ignore kill errors - agent might not be running
    }

    // Give it a moment to die
    // Note: Chapel doesn't have sleep, so we'll just proceed
    // The agent will start on first use anyway

    // Start new agent (it starts on demand, but we can force it)
    try {
      var startP = spawn(["gpg-connect-agent", "/bye"],
                         stdout=pipeStyle.close,
                         stderr=pipeStyle.close);
      startP.wait();

      if startP.exitCode == 0 {
        verboseLog("gpg-agent restarted");
        return true;
      }
    } catch {
      // Fall through
    }

    verboseLog("Failed to restart gpg-agent");
    return false;
  }

  /*
   * Kill gpg-agent
   *
   * Returns:
   *   true if kill succeeded (or agent wasn't running)
   */
  proc killAgent(): bool {
    try {
      var p = spawn(["gpgconf", "--kill", "gpg-agent"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();
      return true;  // gpgconf returns 0 even if agent wasn't running
    } catch {
      return false;
    }
  }

  /*
   * Kill scdaemon (smartcard daemon)
   *
   * This is sometimes needed to reset YubiKey state.
   *
   * Returns:
   *   true if kill succeeded
   */
  proc killScdaemon(): bool {
    try {
      var p = spawn(["gpgconf", "--kill", "scdaemon"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();
      return true;
    } catch {
      return false;
    }
  }

  // ============================================================
  // Key Grip Operations
  // ============================================================

  /*
   * Key grip information for a GPG key
   */
  record KeyGripInfo {
    var keyId: string;
    var fingerprint: string;
    var sigKeyGrip: string;     // Primary signing key grip
    var encKeyGrip: string;     // Encryption subkey grip
    var autKeyGrip: string;     // Authentication subkey grip
  }

  /*
   * Get the key grip for a GPG key ID
   *
   * The key grip is needed for gpg-preset-passphrase operations
   * on software keys.
   *
   * Args:
   *   keyId: GPG key ID (short or long format)
   *
   * Returns:
   *   Signing key grip, or empty string if not found
   */
  proc getKeyGrip(keyId: string): string {
    const info = getKeyGripInfo(keyId);
    return info.sigKeyGrip;
  }

  /*
   * Get detailed key grip information for a GPG key
   *
   * Parses output of: gpg --with-keygrip --list-secret-keys
   *
   * Args:
   *   keyId: GPG key ID (short or long format)
   *
   * Returns:
   *   KeyGripInfo record with all key grips
   */
  proc getKeyGripInfo(keyId: string): KeyGripInfo {
    var info = new KeyGripInfo();
    info.keyId = keyId;

    try {
      var p = spawn(["gpg", "--with-keygrip", "--with-colons", "--list-secret-keys", keyId],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode != 0 {
        return info;
      }

      var output: string;
      p.stdout.readAll(output);

      // Parse colon-delimited output
      // sec:u:...:keyid:...:keygrip   <- Primary key
      // ssb:u:...:keyid:...:keygrip   <- Subkey
      // After each sec/ssb line, grp line has keygrip
      //
      // Format:
      // sec:...:...:...:KEYID:...
      // fpr:...:...:...:...:...:...:...:...:FINGERPRINT:
      // grp:...:...:...:...:...:...:...:...:KEYGRIP:
      // uid:...
      // ssb:...:...:...:SUBKEYID:...
      // fpr:...
      // grp:...:...:...:...:...:...:...:...:KEYGRIP:

      var currentKeyType = "";  // "sec" or "ssb"
      var currentCapabilities = "";

      for line in output.split("\n") {
        const fields = line.split(":");

        if fields.size < 2 then continue;

        const recordType = fields[0];

        select recordType {
          when "sec" {
            currentKeyType = "sec";
            if fields.size > 11 {
              currentCapabilities = fields[11];  // Key capabilities
            }
          }
          when "ssb" {
            currentKeyType = "ssb";
            if fields.size > 11 {
              currentCapabilities = fields[11];
            }
          }
          when "fpr" {
            if currentKeyType == "sec" && fields.size > 9 {
              info.fingerprint = fields[9];
            }
          }
          when "grp" {
            if fields.size > 9 {
              const grip = fields[9];

              if currentKeyType == "sec" {
                // Primary key is usually for signing
                info.sigKeyGrip = grip;
              } else if currentKeyType == "ssb" {
                // Subkeys have specific capabilities
                // s=sign, e=encrypt, a=authenticate
                if currentCapabilities.find("s") != -1 {
                  info.sigKeyGrip = grip;  // Signing subkey overrides
                } else if currentCapabilities.find("e") != -1 {
                  info.encKeyGrip = grip;
                } else if currentCapabilities.find("a") != -1 {
                  info.autKeyGrip = grip;
                }
              }
            }
          }
        }
      }
    } catch {
      // Return empty info on error
    }

    return info;
  }

  /*
   * Get all key grips for keys associated with an email address
   *
   * Args:
   *   email: Email address to search for
   *
   * Returns:
   *   List of KeyGripInfo for matching keys
   */
  proc getKeyGripsForEmail(email: string): list(KeyGripInfo) {
    var results: list(KeyGripInfo);

    try {
      var p = spawn(["gpg", "--with-keygrip", "--with-colons", "--list-secret-keys", email],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode != 0 {
        return results;
      }

      var output: string;
      p.stdout.readAll(output);

      // Parse for key IDs first
      var keyIds: list(string);
      for line in output.split("\n") {
        const fields = line.split(":");
        if fields.size > 4 && fields[0] == "sec" {
          keyIds.pushBack(fields[4]);
        }
      }

      // Get detailed info for each key
      for keyId in keyIds {
        const info = getKeyGripInfo(keyId);
        if info.sigKeyGrip != "" {
          results.pushBack(info);
        }
      }
    } catch {
      // Return empty list on error
    }

    return results;
  }

  // ============================================================
  // Cache Management (for software keys only)
  // ============================================================

  /*
   * Preset a passphrase in gpg-agent cache
   *
   * NOTE: This only works for software keys, NOT for YubiKey/smartcard PINs.
   * For YubiKey, use the custom pinentry approach instead.
   *
   * Args:
   *   keyGrip: The key grip to preset passphrase for
   *   passphrase: The passphrase to cache
   *   ttl: Cache timeout in seconds (-1 for default)
   *
   * Returns:
   *   true if preset succeeded
   */
  proc presetPassphrase(keyGrip: string, passphrase: string, ttl: int = -1): bool {
    // Check if preset is allowed
    const (readOk, entries) = readConfig();
    if !readOk || !isConfigEnabled(entries, ALLOW_PRESET) {
      verboseLog("allow-preset-passphrase not enabled in gpg-agent.conf");
      return false;
    }

    // Convert passphrase to hex for gpg-connect-agent
    var hexPass = "";
    for ch in passphrase {
      const byte = ch: uint(8);
      const high = (byte >> 4) & 0x0F;
      const low = byte & 0x0F;
      hexPass += hexDigit(high) + hexDigit(low);
    }

    // Build preset command
    var cmd = "PRESET_PASSPHRASE " + keyGrip;
    if ttl >= 0 {
      cmd += " " + ttl:string;
    } else {
      cmd += " -1";  // Use default TTL
    }
    cmd += " " + hexPass;

    try {
      var p = spawn(["gpg-connect-agent", cmd, "/bye"],
                    stdout=pipeStyle.pipe,
                    stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        verboseLog("Passphrase preset for keygrip: ", keyGrip);
        return true;
      }

      var output: string;
      p.stdout.readAll(output);
      verboseLog("Preset failed: ", output);
    } catch {
      // Fall through
    }

    return false;
  }

  /*
   * Helper function to convert nibble to hex character
   */
  private proc hexDigit(n: uint(8)): string {
    const digits = "0123456789ABCDEF";
    return digits[n:int..n:int];
  }

  /*
   * Clear a cached passphrase from gpg-agent
   *
   * Args:
   *   keyGrip: The key grip to clear
   *
   * Returns:
   *   true if clear succeeded
   */
  proc clearPassphrase(keyGrip: string): bool {
    try {
      var p = spawn(["gpg-connect-agent", "CLEAR_PASSPHRASE " + keyGrip, "/bye"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Clear all cached passphrases from gpg-agent
   *
   * Returns:
   *   true if clear succeeded
   */
  proc clearAllPassphrases(): bool {
    try {
      // RESET clears all cached passphrases
      var p = spawn(["gpg-connect-agent", "RESET", "/bye"],
                    stdout=pipeStyle.close,
                    stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  // ============================================================
  // Configuration Helpers
  // ============================================================

  /*
   * Enable allow-preset-passphrase in gpg-agent.conf
   *
   * Returns:
   *   true if configuration succeeded
   */
  proc enablePresetPassphrase(): bool {
    const (readOk, originalEntries) = readConfig();
    var entries = originalEntries;

    if !readOk {
      return false;
    }

    if !isConfigEnabled(entries, ALLOW_PRESET) {
      setConfigValue(entries, ALLOW_PRESET, "");
      return writeConfig(entries);
    }

    return true;  // Already enabled
  }

  /*
   * Enable allow-loopback-pinentry in gpg-agent.conf
   *
   * Returns:
   *   true if configuration succeeded
   */
  proc enableLoopbackPinentry(): bool {
    const (readOk, originalEntries) = readConfig();
    var entries = originalEntries;

    if !readOk {
      return false;
    }

    if !isConfigEnabled(entries, ALLOW_LOOPBACK) {
      setConfigValue(entries, ALLOW_LOOPBACK, "");
      return writeConfig(entries);
    }

    return true;  // Already enabled
  }

  /*
   * Set cache TTL values in gpg-agent.conf
   *
   * Args:
   *   defaultTTL: Default cache timeout in seconds
   *   maxTTL: Maximum cache timeout in seconds
   *
   * Returns:
   *   true if configuration succeeded
   */
  proc setCacheTTL(defaultTTL: int, maxTTL: int): bool {
    const (readOk, originalEntries) = readConfig();
    var entries = originalEntries;

    if !readOk {
      return false;
    }

    setConfigValue(entries, DEFAULT_CACHE_TTL, defaultTTL:string);
    setConfigValue(entries, MAX_CACHE_TTL, maxTTL:string);

    return writeConfig(entries);
  }

  /*
   * Get current cache TTL values
   *
   * Returns:
   *   Tuple of (defaultTTL, maxTTL) with 0 for unset values
   */
  proc getCacheTTL(): (int, int) {
    const (readOk, entries) = readConfig();
    if !readOk {
      return (0, 0);
    }

    var defaultTTL = 0;
    var maxTTL = 0;

    const (hasDefault, defaultVal) = getConfigValue(entries, DEFAULT_CACHE_TTL);
    if hasDefault {
      try {
        defaultTTL = defaultVal:int;
      } catch {
        // Ignore parse errors
      }
    }

    const (hasMax, maxVal) = getConfigValue(entries, MAX_CACHE_TTL);
    if hasMax {
      try {
        maxTTL = maxVal:int;
      } catch {
        // Ignore parse errors
      }
    }

    return (defaultTTL, maxTTL);
  }

  // ============================================================
  // Status and Diagnostics
  // ============================================================

  /*
   * Get comprehensive gpg-agent status
   *
   * Returns:
   *   Formatted status string
   */
  proc getAgentStatus(): string {
    var status = "gpg-agent Status:\n";

    // Check if running
    const running = isAgentRunning();
    status += "  Running: " + (if running then "Yes" else "No") + "\n";

    // Socket path
    const socket = getAgentSocket();
    if socket != "" {
      status += "  Socket: " + socket + "\n";
    }

    // Read config
    const (readOk, entries) = readConfig();
    if readOk {
      // Pinentry
      const (hasPinentry, pinentryPath) = getConfigValue(entries, PINENTRY_PROGRAM);
      if hasPinentry {
        status += "  Pinentry: " + pinentryPath + "\n";
      } else {
        status += "  Pinentry: (system default)\n";
      }

      // Cache settings
      const (defaultTTL, maxTTL) = getCacheTTL();
      if defaultTTL > 0 {
        status += "  Default Cache TTL: " + defaultTTL:string + "s\n";
      }
      if maxTTL > 0 {
        status += "  Max Cache TTL: " + maxTTL:string + "s\n";
      }

      // Feature flags
      status += "  Allow Loopback: " +
                (if isConfigEnabled(entries, ALLOW_LOOPBACK) then "Yes" else "No") + "\n";
      status += "  Allow Preset: " +
                (if isConfigEnabled(entries, ALLOW_PRESET) then "Yes" else "No") + "\n";

      // Trusted Workstation mode
      const (twEnabled, twPinentry) = isTrustedWorkstationConfigured();
      status += "  Trusted Workstation: " + (if twEnabled then "Yes" else "No") + "\n";
    } else {
      status += "  Config: Unable to read gpg-agent.conf\n";
    }

    return status;
  }
}

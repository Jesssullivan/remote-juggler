/*
 * RemoteJuggler - Backend-agnostic git identity management
 *
 * Main entry point and CLI interface.
 * Supports CLI mode (default), MCP server mode, and ACP server mode.
 *
 * Copyright (c) 2026 Jess Sullivan
 * SPDX-License-Identifier: Zlib
 */
prototype module remote_juggler {
  // Standard library imports
  use IO;
  use List;
  use Map;
  use Time;
  use CTypes;
  use Path;
  use FileSystem;

  // Include and re-export submodules
  // These are located in src/remote_juggler/ directory
  include module Core;
  include module Config;
  include module GlobalConfig;
  include module State;
  include module Keychain;
  include module ProviderCLI;
  include module GPG;
  include module Remote;
  include module Identity;
  include module TokenHealth;
  include module Protocol;
  include module MCP;
  include module ACP;
  include module YubiKey;
  include module HSM;
  include module KeePassXC;
  include module Manifest;
  include module AgeEncryption;
  include module GPGAgent;
  include module Tools;
  include module TrustedWorkstation;
  include module Setup;

  // Public re-exports for external consumers
  public use Core;
  public use Config;
  public use GlobalConfig;
  public use State;
  public use Keychain;
  public use ProviderCLI;
  public use GPG;
  public use Remote;
  public use Identity;
  public use TokenHealth;
  public use Protocol;
  public use MCP;
  public use ACP;
  public use Tools;
  public use YubiKey;
  public use HSM;
  public use TrustedWorkstation;
  public use KeePassXC;
  public use Manifest;
  public use AgeEncryption;
  public use GPGAgent;

  // ==========================================================================
  // ANSI Color/Formatting Helpers
  // ==========================================================================

  // Convenience wrappers using Core.colorize
  // These provide a cleaner API for common color operations

  proc green(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Green);
  }

  proc red(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Red);
  }

  proc yellow(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Yellow);
  }

  proc blue(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Blue);
  }

  proc cyan(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Cyan);
  }

  proc magenta(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Magenta);
  }

  proc bold(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Bold);
  }

  proc dim(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Dim);
  }

  proc underline(s: string): string {
    return Core.colorize(s, Core.ANSIColor.Underline);
  }

  // ==========================================================================
  // List Helpers
  // ==========================================================================

  // Create a sublist from index start to end
  proc sublist(lst: list(string), start: int): list(string) {
    var result = new list(string);
    for i in start..<lst.size {
      result.pushBack(lst[i]);
    }
    return result;
  }

  // Create an empty list for arguments
  proc emptyArgs(): list(string) {
    return new list(string);
  }

  // ==========================================================================
  // Output Formatting
  // ==========================================================================

  proc printSuccess(message: string) {
    writeln(green("[OK]"), " ", message);
  }

  proc printError(message: string) {
    writeln(red("[ERROR]"), " ", message);
  }

  proc printWarning(message: string) {
    writeln(yellow("[WARN]"), " ", message);
  }

  proc printInfo(message: string) {
    writeln(cyan("[INFO]"), " ", message);
  }

  proc printDebug(message: string) {
    if verbose {
      writeln(dim("[DEBUG]"), " ", message);
    }
  }

  // Print header with version
  proc printHeader() {
    writeln(bold("RemoteJuggler"), " v", Core.VERSION);
    writeln();
  }

  // Print usage/help information
  proc printUsage() {
    printHeader();

    writeln(bold("USAGE:"));
    writeln("  remote-juggler [OPTIONS] <COMMAND> [ARGS]");
    writeln();

    writeln(bold("OPTIONS:"));
    writeln("  --mode=<mode>     Operation mode: cli (default), mcp, acp");
    writeln("  --verbose         Enable verbose output");
    writeln("  --help            Show this help message");
    writeln("  --configPath=<p>  Override config file path");
    writeln("  --useKeychain     Enable/disable keychain (Darwin, default: true)");
    writeln("  --gpgSign         Enable/disable GPG signing (default: true)");
    writeln("  --provider=<p>    Filter by provider: gitlab, github, bitbucket, all");
    writeln();

    writeln(bold("COMMANDS:"));
    writeln();

    writeln("  ", bold("Identity Management:"));
    writeln("    list              List all configured identities");
    writeln("    detect            Detect identity for current repository");
    writeln("    switch <name>     Switch to identity (alias: 'to')");
    writeln("    to <name>         Alias for switch");
    writeln("    validate <name>   Test SSH/API connectivity for identity");
    writeln("    verify            Verify identity matches expected for repo");
    writeln("    status            Show current identity status");
    writeln();

    writeln("  ", bold("Configuration:"));
    writeln("    config show       Display configuration");
    writeln("    config add <n>    Add new identity interactively");
    writeln("    config edit <n>   Edit existing identity");
    writeln("    config remove <n> Remove identity");
    writeln("    config import     Import identities from SSH config");
    writeln("    config sync       Synchronize managed blocks");
    writeln();

    writeln("  ", bold("Setup & Integration:"));
    writeln("    setup             Interactive first-time setup wizard");
    writeln("    setup --auto      Auto-detect SSH hosts and GPG keys");
    writeln("    setup --shell     Install shell integrations (envrc, starship, nix)");
    writeln("    setup --status    Show current setup status");
    writeln();

    writeln("  ", bold("Token Management:"));
    writeln("    token set <n>     Store token in keychain (Darwin)");
    writeln("    token get <n>     Retrieve token (masked output)");
    writeln("    token clear <n>   Remove token from keychain");
    writeln("    token verify      Test all configured credentials");
    writeln("    token check-expiry [n]  Check token expiration status");
    writeln("    token renew <n>   Renew expired/expiring token");
    writeln();

    writeln("  ", bold("GPG Signing:"));
    writeln("    gpg status        Show GPG configuration");
    writeln("    gpg configure <n> Configure GPG for identity");
    writeln("    gpg verify        Check provider registration");
    writeln();

    writeln("  ", bold("PIN Management (Trusted Workstation):"));
    writeln("    pin store <n>     Store YubiKey PIN in HSM (TPM/SecureEnclave)");
    writeln("    pin clear <n>     Remove stored PIN from HSM");
    writeln("    pin status [n]    Check PIN storage status");
    writeln("    security-mode <m> Set security mode: maximum_security,");
    writeln("                      developer_workflow, trusted_workstation");
    writeln();

    writeln("  ", bold("YubiKey Management:"));
    writeln("    yubikey info                    Show YubiKey device info");
    writeln("    yubikey set-pin-policy <p>      Set PIN policy (once|always)");
    writeln("    yubikey set-touch <s> <p>       Set touch policy for slot");
    writeln("                                    Slots: sig, enc, aut");
    writeln("                                    Policies: on, off, cached");
    writeln("    yubikey configure-trusted       Configure for Trusted Workstation");
    writeln("    yubikey diagnostics             Run diagnostic checks");
    writeln();

    writeln("  ", bold("Trusted Workstation Mode:"));
    writeln("    trusted-workstation enable <n>  Enable mode for identity (prompts for PIN)");
    writeln("    trusted-workstation disable <n> Disable mode for identity");
    writeln("    trusted-workstation status [n]  Show current TWS status");
    writeln("    trusted-workstation verify <n>  Verify mode is working (test sign)");
    writeln();

    writeln("  ", bold("Key Store (KeePassXC):"));
    writeln("    keys init         Bootstrap a new kdbx credential database");
    writeln("    keys status       Show key store status");
    writeln("    keys search <q>   Fuzzy search across all entries");
    writeln("    keys search <q> --json  Search with JSON output");
    writeln("    keys resolve <q>  Search and retrieve in one step");
    writeln("    keys get <path>   Retrieve a secret by entry path");
    writeln("    keys store <path> Store a secret at entry path");
    writeln("    keys delete <p>   Delete an entry by path");
    writeln("    keys list [group] List entries in a group");
    writeln("    keys ingest <f>   Ingest a .env file into the key store");
    writeln("    keys crawl [dirs] Crawl directories for .env files");
    writeln("    keys discover     Auto-discover credentials (env, ssh)");
    writeln("    keys export <grp> Export group as .env or JSON");
    writeln("    keys sops-ingest <f>  Ingest a SOPS-encrypted file");
    writeln("    keys sops-sync <f>    Sync SOPS file (add/update/delete)");
    writeln("    keys sops-export      Export age key for SOPS recipients");
    writeln();

    writeln("  ", bold("Debug:"));
    writeln("    debug ssh-config  Show parsed SSH configuration");
    writeln("    debug git-config  Show parsed gitconfig rewrites");
    writeln("    debug keychain    Test keychain access");
    writeln("    debug hsm         Test HSM (TPM/SecureEnclave) access");
    writeln();

    writeln(bold("SERVER MODES:"));
    writeln("  remote-juggler --mode=mcp    Run as MCP STDIO server");
    writeln("  remote-juggler --mode=acp    Run as ACP STDIO server");
    writeln();

    writeln(bold("EXAMPLES:"));
    writeln("  remote-juggler                       # Show current status");
    writeln("  remote-juggler list                  # List all identities");
    writeln("  remote-juggler list --provider=gitlab");
    writeln("  remote-juggler switch personal       # Switch to personal identity");
    writeln("  remote-juggler to work               # Switch to work identity");
    writeln("  remote-juggler token set personal    # Store token in keychain");
    writeln("  remote-juggler --mode=mcp            # Start MCP server");
    writeln();

    writeln(dim("For more information: https://gitlab.com/tinyland/projects/remote-juggler"));
  }

  // Print a table of identities
  proc printIdentityTable(identities: list(GitIdentity)) {
    if identities.size == 0 {
      writeln(dim("No identities configured."));
      writeln();
      writeln("Run 'remote-juggler config import' to import from SSH config");
      writeln("or 'remote-juggler config add <name>' to add manually.");
      return;
    }

    // Calculate column widths
    var nameWidth = 8;   // "Identity"
    var providerWidth = 8; // "Provider"
    var hostWidth = 8;   // "SSH Host"
    var userWidth = 4;   // "User"
    var emailWidth = 5;  // "Email"

    for identity in identities {
      if identity.name.size > nameWidth then nameWidth = identity.name.size;
      const provStr = providerToString(identity.provider);
      if provStr.size > providerWidth then providerWidth = provStr.size;
      if identity.host.size > hostWidth then hostWidth = identity.host.size;
      if identity.user.size > userWidth then userWidth = identity.user.size;
      if identity.email.size > emailWidth then emailWidth = identity.email.size;
    }

    // Add padding
    nameWidth += 2;
    providerWidth += 2;
    hostWidth += 2;
    userWidth += 2;
    emailWidth += 2;

    // Header - use simple write to avoid format string issues with ANSI codes
    write(padRight(bold("Identity"), nameWidth));
    write(padRight(bold("Provider"), providerWidth));
    write(padRight(bold("SSH Host"), hostWidth));
    write(padRight(bold("User"), userWidth));
    write(padRight(bold("Email"), emailWidth));
    writeln(bold("GPG"));

    // Separator
    write(repeatChar("-", nameWidth + providerWidth + hostWidth + userWidth + emailWidth + 12));
    writeln();

    // Get current identity for highlighting
    const currentCtx = State.loadState();

    // Rows
    for identity in identities {
      const isCurrent = (identity.name == currentCtx.currentIdentity);
      const marker = if isCurrent then green("*") else " ";
      const nameStr = if isCurrent then green(identity.name) else identity.name;
      const gpgStatus = if identity.gpg.keyId != "" && identity.gpg.keyId != "none"
                        then green("Yes")
                        else dim("No");

      // Use simple write to avoid format string issues with ANSI codes
      write(marker);
      write(padRight(nameStr, nameWidth - 1));
      write(padRight(providerToString(identity.provider), providerWidth));
      write(padRight(identity.host, hostWidth));
      write(padRight(identity.user, userWidth));
      write(padRight(identity.email, emailWidth));
      writeln(gpgStatus);
    }
  }

  // Helper to pad string to width (right-aligned padding on left)
  proc padRight(s: string, width: int): string {
    // Calculate visible length (ignoring ANSI codes)
    var visibleLen = 0;
    var inEscape = false;
    for ch in s {
      if ch == '\x1b' then inEscape = true;
      else if inEscape && ch == 'm' then inEscape = false;
      else if !inEscape then visibleLen += 1;
    }

    if visibleLen >= width then return s + " ";

    var padding = "";
    for i in 1..(width - visibleLen) {
      padding += " ";
    }
    return s + padding;
  }

  // Helper to repeat a character
  proc repeatChar(c: string, count: int): string {
    var result = "";
    for i in 1..count {
      result += c;
    }
    return result;
  }

  // Print detailed status
  proc printStatus(ctx: SwitchContext, identity: GitIdentity) {
    printHeader();

    writeln(bold("Current Identity: "), green(identity.name),
            " (", providerToString(identity.provider), ")");

    writeln("  User:     ", identity.user, " <", identity.email, ">");
    writeln("  SSH Host: ", identity.host);

    // GPG status
    if identity.gpg.keyId != "" && identity.gpg.keyId != "none" {
      const signStatus = if identity.gpg.signCommits then green("commits signed") else dim("signing disabled");
      writeln("  GPG Key:  ", identity.gpg.keyId, " (", signStatus, ")");
    } else {
      writeln("  GPG Key:  ", dim("not configured"));
    }

    // Credential source
    write("  Auth:     ");
    select identity.credentialSource {
      when Core.CredentialSource.Keychain do writeln(green("Keychain"));
      when Core.CredentialSource.KeePassXC do writeln(cyan("KeePassXC (" + identity.keePassEntry + ")"));
      when Core.CredentialSource.Environment do writeln(cyan("Environment ($" + identity.tokenEnvVar + ")"));
      when Core.CredentialSource.CLIAuth do writeln(cyan("CLI Auth (glab/gh)"));
      when Core.CredentialSource.None do writeln(yellow("SSH-only"));
    }

    writeln();

    // Repository info (if in a git repo)
    const (isRepo, repoRoot) = Remote.getRepositoryRoot(".");
    if isRepo {
      writeln(bold("Repository: "), repoRoot);

      const (hasRemote, remoteUrl) = Remote.getOriginURL(repoRoot);
      if hasRemote {
        writeln("  Remote:   origin -> ", dim(remoteUrl));
      }

      const (hasBranch, branchName) = Remote.getCurrentBranch(repoRoot);
      if hasBranch {
        const (hasUpstream, upstream) = Remote.getUpstreamBranch(repoRoot, branchName);
        if hasUpstream {
          writeln("  Branch:   ", branchName, " -> ", dim(upstream));
        } else {
          writeln("  Branch:   ", branchName, " ", dim("(no upstream)"));
        }
      }

      // Check if remote matches identity using Identity module
      if hasRemote {
        const (detected, detectedIdentity, _) = Identity.detectIdentity(repoRoot);
        if detected && detectedIdentity.name != identity.name {
          writeln();
          printWarning("Remote URL suggests identity '" + detectedIdentity.name + "'");
          writeln("  Run 'remote-juggler switch ", detectedIdentity.name, "' to match");
        }
      }
    } else {
      writeln(dim("Not in a git repository"));
    }

    // Last switch time
    if ctx.lastSwitch != "" {
      writeln();
      writeln(dim("Last switched: "), dim(ctx.lastSwitch));
    }
  }

  // Print simple status line (for detect command)
  proc printDetectedIdentity(identityName: string, confidence: string, reasons: list(string)) {
    writeln(bold("Detected Identity: "), green(identityName));
    writeln("  Confidence: ", confidence);

    if reasons.size > 0 {
      writeln("  Reasons:");
      for reason in reasons {
        writeln("    - ", reason);
      }
    }
  }

  // ==========================================================================
  // Command Handlers
  // ==========================================================================

  // Handle 'list' command
  proc handleList(args: list(string)) {
    printDebug("Executing list command");

    // Load identities - filter by provider if specified
    var identities: list(GitIdentity);
    if provider != "all" {
      const targetProvider = stringToProvider(provider);
      identities = Identity.listIdentities(targetProvider);
    } else {
      // Pass Custom as "all" filter per Identity module convention
      identities = Identity.listIdentities(Core.Provider.Custom);
    }

    printIdentityTable(identities);
  }

  // Handle 'detect' command
  proc handleDetect(args: list(string)) {
    printDebug("Executing detect command");

    const repoPath = if args.size > 0 then args[0] else ".";

    if !Remote.isGitRepository(repoPath) {
      printError("Not in a git repository");
      return;
    }

    const (hasRemote, remoteUrl) = Remote.getOriginURL(repoPath);
    if !hasRemote {
      printError("No remote 'origin' found");
      return;
    }

    // Use the detectIdentityDetailed from Identity module
    const detection = Identity.detectIdentityDetailed(repoPath);

    if !detection.found {
      printWarning("Could not detect identity from remote URL");
      writeln("  Remote URL: ", remoteUrl);
      writeln("  Reason: ", detection.reason);
      writeln();
      writeln("Run 'remote-juggler config import' to import SSH identities");
      return;
    }

    var reasons: list(string);
    reasons.pushBack(detection.reason);
    printDetectedIdentity(detection.identity.name, detection.confidence, reasons);
  }

  // Handle 'switch' / 'to' command
  proc handleSwitch(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler switch <identity>");
      return;
    }

    const targetIdentity = args[0];
    printDebug("Switching to identity: " + targetIdentity);

    // Check if identity exists
    const (found, identity) = Identity.getIdentity(targetIdentity);
    if !found {
      printError("Identity not found: " + targetIdentity);
      writeln();
      writeln("Available identities:");
      const identities = Identity.listIdentityNames();
      for name in identities {
        writeln("  - ", name);
      }
      return;
    }

    // Perform the switch using the switchIdentity function
    const result = Identity.switchIdentity(targetIdentity, true, ".");

    if result.success {
      printSuccess("Switched to " + result.identity.name);
      writeln();
      writeln("  Provider: ", providerToString(result.identity.provider));
      writeln("  User:     ", result.identity.user, " <", result.identity.email, ">");
      writeln("  SSH Host: ", result.identity.host);

      // Auth status
      write("  Auth:     ");
      select result.authMode {
        when Core.AuthMode.KeychainAuth do writeln(green("Keychain authenticated"));
        when Core.AuthMode.CLIAuthenticated do writeln(green("CLI authenticated"));
        when Core.AuthMode.TokenOnly do writeln(cyan("Token available"));
        when Core.AuthMode.SSHOnly do writeln(yellow("SSH-only mode"));
      }

      // GPG status
      if result.gpgConfigured {
        writeln("  GPG:      ", green("Signing configured"));
      }

      // Show remote update status
      if result.remoteUpdated {
        writeln("  Remote:   ", green("Updated for identity"));
      }

      // Check for token expiry warnings
      writeln();
      TokenHealth.warnIfExpiring(result.identity);

    } else {
      printError("Failed to switch: " + result.message);
    }
  }

  // Handle 'validate' command
  proc handleValidate(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler validate <identity>");
      return;
    }

    const targetIdentity = args[0];
    printDebug("Validating identity: " + targetIdentity);

    // Use Identity module's validation
    const result = Identity.validateIdentity(targetIdentity, gpgSign);

    if result.issues.size > 0 && !result.sshConnectivity {
      printError("Identity not found or validation failed: " + targetIdentity);
      for issue in result.issues {
        writeln("  ", issue);
      }
      return;
    }

    // Get the identity for display
    const (found, identity) = Identity.getIdentity(targetIdentity);
    if !found {
      printError("Identity not found: " + targetIdentity);
      return;
    }

    writeln(bold("Validating: "), identity.name, " (", providerToString(identity.provider), ")");
    writeln();

    // SSH connectivity test
    write("  SSH Connection... ");
    if result.sshConnectivity {
      writeln(green("OK"));
    } else {
      writeln(red("FAILED"));
    }

    // Credential test
    write("  Credentials...    ");
    if result.credentialAvailable {
      writeln(green("OK"));
    } else {
      writeln(yellow("Not found"), " (SSH-only mode)");
    }

    // GPG key test
    if identity.gpg.isConfigured() {
      write("  GPG Key...        ");
      if result.gpgKeyFound {
        writeln(green("OK"));
      } else {
        writeln(red("FAILED"));
      }

      // Check provider registration
      write("  GPG Registered... ");
      if result.gpgRegistered {
        writeln(green("OK"));
      } else {
        writeln(yellow("Not verified"));
        writeln("    Add at: ", GPG.getGPGSettingsURL(identity));
      }
    }

    // Check token expiry if credentials are available
    if result.credentialAvailable {
      write("  Token Expiry...   ");
      const healthResult = TokenHealth.checkTokenHealth(identity);
      if healthResult.isExpired {
        writeln(red("EXPIRED"));
      } else if healthResult.needsRenewal {
        writeln(yellow("Expiring soon"), " (", healthResult.daysUntilExpiry, " days)");
      } else if healthResult.daysUntilExpiry < 999999 {
        writeln(green("OK"), " ", dim("(" + healthResult.daysUntilExpiry:string + " days)"));
      } else {
        writeln(green("OK"), " ", dim("(unknown expiry)"));
      }
    }

    // Show any issues
    if result.issues.size > 0 {
      writeln();
      writeln(yellow("Issues found:"));
      for issue in result.issues {
        writeln("  - ", issue);
      }
    }

    writeln();
  }

  // Handle 'verify' command (pre-commit hook verification)
  proc handleVerify(args: list(string)) {
    printDebug("Executing verify command");

    var isPreCommit = false;
    var isPrePush = false;

    // Parse flags
    for arg in args {
      if arg == "--pre-commit" then isPreCommit = true;
      else if arg == "--pre-push" then isPrePush = true;
    }

    const repoPath = ".";

    // Check if we're in a git repo
    if !Remote.isGitRepository(repoPath) {
      if !isPreCommit {
        printError("Not in a git repository");
      }
      halt(1);
    }

    // Get current git config user.email
    const (gotEmail, currentEmail) = Remote.getGitConfig(repoPath, "user.email");
    if !gotEmail || currentEmail == "" {
      if !isPreCommit {
        printError("No git user.email configured");
      }
      halt(1);
    }

    // Detect expected identity for this repo
    const (found, expectedIdentity, reason) = Identity.detectIdentity(repoPath);
    if !found {
      // No identity matched, cannot verify
      if !isPreCommit {
        printWarning("Could not detect expected identity for this repository");
        writeln("Run 'remote-juggler config import' to import identities");
      }
      // In pre-commit mode, allow commits if no identity is matched (user may have unconfigured repos)
      if isPreCommit {
        halt(0);
      }
      halt(1);
    }

    // Compare current git config with expected identity
    if currentEmail != expectedIdentity.email {
      if isPreCommit {
        stderr.writeln("[RemoteJuggler] Identity mismatch!");
        stderr.writeln("  Current:  ", currentEmail);
        stderr.writeln("  Expected: ", expectedIdentity.email, " (", expectedIdentity.name, ")");
      } else {
        printError("Identity mismatch!");
        writeln("  Current git config: ", currentEmail);
        writeln("  Expected identity:  ", expectedIdentity.email, " (", expectedIdentity.name, ")");
        writeln();
        writeln("Run: remote-juggler switch ", expectedIdentity.name);
      }
      halt(1);
    }

    // Verification passed
    if !isPreCommit {
      printSuccess("Identity verified: " + expectedIdentity.name);
      writeln("  Email: ", expectedIdentity.email);
      writeln("  Reason: ", reason);
    }
    // Exit 0 on success (implicit)
  }

  // Handle 'status' command
  proc handleStatus() {
    printDebug("Executing status command");

    const ctx = State.loadState();

    if ctx.currentIdentity == "" || !ctx.hasActiveIdentity() {
      printHeader();
      writeln(yellow("No identity currently active."));
      writeln();
      writeln("Run 'remote-juggler detect' to detect based on repository");
      writeln("or 'remote-juggler switch <identity>' to set one.");
      return;
    }

    const (found, identity) = Identity.getIdentity(ctx.currentIdentity);
    if !found {
      printHeader();
      printWarning("Current identity '" + ctx.currentIdentity + "' not found in config");
      return;
    }

    printStatus(ctx, identity);
  }

  // Handle 'config' subcommands
  proc handleConfig(args: list(string)) {
    if args.size < 1 {
      // Default to show
      handleConfigShow(emptyArgs());
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "show" do handleConfigShow(subArgs);
      when "add" do handleConfigAdd(subArgs);
      when "edit" do handleConfigEdit(subArgs);
      when "remove", "rm", "delete" do handleConfigRemove(subArgs);
      when "import" do handleConfigImport();
      when "sync" do handleConfigSync();
      when "init" do handleConfigInit();
      otherwise {
        printError("Unknown config subcommand: " + subcommand);
        writeln("Available: show, add, edit, remove, import, sync");
      }
    }
  }

  proc handleConfigShow(args: list(string)) {
    printDebug("Showing configuration");

    const configPath = GlobalConfig.getConfigPath();
    writeln(bold("Configuration: "), configPath);
    writeln();

    // Check if specific section requested
    if args.size > 0 {
      const section = args[0];
      select section {
        when "identities" {
          const identities = GlobalConfig.loadIdentities();
          printIdentityTable(identities);
        }
        when "settings" {
          const settings = GlobalConfig.loadSettings();
          writeln(bold("Settings:"));
          writeln("  defaultProvider:     ", settings.defaultProvider);
          writeln("  autoDetect:          ", settings.autoDetect);
          writeln("  useKeychain:         ", settings.useKeychain);
          writeln("  gpgSign:             ", settings.gpgSign);
          writeln("  gpgVerifyWithProvider: ", settings.gpgVerifyWithProvider);
          writeln("  fallbackToSSH:       ", settings.fallbackToSSH);
        }
        when "ssh-hosts", "ssh" {
          const hosts = GlobalConfig.getManagedSSHHosts();
          writeln(bold("Managed SSH Hosts:"));
          writeln(dim("  (Auto-generated from ~/.ssh/config)"));
          writeln();
          for h in hosts {
            writeln("  ", bold(h.host));
            writeln("    Hostname: ", h.hostname);
            writeln("    Key:      ", h.identityFile);
          }
        }
        when "rewrites", "git" {
          const rewrites = GlobalConfig.getManagedGitRewrites();
          writeln(bold("Git URL Rewrites:"));
          writeln(dim("  (Auto-generated from ~/.gitconfig)"));
          writeln();
          for rewrite in rewrites {
            writeln("  ", rewrite.fromURL, " -> ", rewrite.toURL);
          }
        }
        otherwise {
          printError("Unknown section: " + section);
          writeln("Available: identities, settings, ssh-hosts, rewrites");
        }
      }
      return;
    }

    // Show full configuration summary
    const identities = GlobalConfig.loadIdentities();
    const settings = GlobalConfig.loadSettings();

    writeln(bold("Identities: "), identities.size, " configured");
    printIdentityTable(identities);

    writeln();
    writeln(bold("Settings:"));
    writeln("  Auto-detect: ", if settings.autoDetect then green("enabled") else dim("disabled"));
    writeln("  Keychain:    ", if settings.useKeychain then green("enabled") else dim("disabled"));
    writeln("  GPG Sign:    ", if settings.gpgSign then green("enabled") else dim("disabled"));
  }

  proc handleConfigAdd(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler config add <name>");
      return;
    }

    const name = args[0];
    printDebug("Adding identity: " + name);

    // Check if already exists
    const existing = GlobalConfig.getIdentity(name);
    if existing.name != "" {
      printError("Identity already exists: " + name);
      writeln("Use 'remote-juggler config edit ", name, "' to modify it");
      return;
    }

    writeln(bold("Adding identity: "), name);
    writeln();

    // Interactive prompts (basic implementation)
    // In a full implementation, this would use proper interactive input
    writeln("Please edit the config file directly at:");
    writeln("  ", GlobalConfig.getConfigPath());
    writeln();
    writeln("Or use 'remote-juggler config import' to auto-detect from SSH config.");
  }

  proc handleConfigEdit(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler config edit <name>");
      return;
    }

    const name = args[0];
    printDebug("Editing identity: " + name);

    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    writeln(bold("Edit identity: "), name);
    writeln();
    writeln("Current configuration:");
    writeln("  Provider: ", providerToString(identity.provider));
    writeln("  Host:     ", identity.host);
    writeln("  Hostname: ", identity.hostname);
    writeln("  User:     ", identity.user);
    writeln("  Email:    ", identity.email);
    writeln();
    writeln("Please edit the config file directly at:");
    writeln("  ", GlobalConfig.getConfigPath());
  }

  proc handleConfigRemove(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler config remove <name>");
      return;
    }

    const name = args[0];
    printDebug("Removing identity: " + name);

    const existing = GlobalConfig.getIdentity(name);
    if existing.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    const result = GlobalConfig.removeIdentity(name);
    if result {
      printSuccess("Removed identity: " + name);
    } else {
      printError("Failed to remove identity");
    }
  }

  proc handleConfigImport() {
    printDebug("Importing identities from SSH config");

    writeln(bold("Importing from SSH config..."));
    writeln();

    const result = GlobalConfig.importFromSSHConfig();

    if result.imported > 0 {
      printSuccess("Imported " + result.imported:string + " identities");
      writeln();
      for name in result.names {
        writeln("  - ", name);
      }
    } else if result.skipped > 0 {
      printInfo("No new identities to import (" + result.skipped:string + " already exist)");
    } else {
      printWarning("No git-related SSH hosts found in ~/.ssh/config");
    }
  }

  proc handleConfigSync() {
    printDebug("Synchronizing managed blocks");

    writeln(bold("Synchronizing configuration..."));
    writeln();

    const result = GlobalConfig.syncManagedBlocks();

    if result.sshHostsUpdated {
      printSuccess("SSH hosts updated");
    } else {
      writeln("  SSH hosts: ", dim("up to date"));
    }

    if result.gitRewritesUpdated {
      printSuccess("Git rewrites updated");
    } else {
      writeln("  Git rewrites: ", dim("up to date"));
    }
  }

  proc handleConfigInit() {
    printDebug("Initializing configuration");

    const result = GlobalConfig.initializeConfig();
    if result {
      printSuccess("Configuration initialized at:");
      writeln("  ", GlobalConfig.getConfigPath());
    } else {
      printError("Failed to initialize configuration");
    }
  }

  // Handle 'token' subcommands
  proc handleToken(args: list(string)) {
    if args.size < 1 {
      printError("Missing subcommand");
      writeln("Usage: remote-juggler token <set|get|clear|verify|check-expiry|renew> [identity]");
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "set" do handleTokenSet(subArgs);
      when "get" do handleTokenGet(subArgs);
      when "clear", "delete", "rm" do handleTokenClear(subArgs);
      when "verify", "test" do handleTokenVerify();
      when "check-expiry", "expiry", "check" do handleTokenCheckExpiry(subArgs);
      when "renew", "refresh" do handleTokenRenew(subArgs);
      otherwise {
        printError("Unknown token subcommand: " + subcommand);
        writeln("Available: set, get, clear, verify, check-expiry, renew");
      }
    }
  }

  proc handleTokenSet(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler token set <identity>");
      return;
    }

    const name = args[0];
    printDebug("Setting token for: " + name);

    // Check if keychain is available
    if !Keychain.isDarwin() {
      printError("Keychain integration requires macOS");
      writeln("Use environment variables or CLI auth instead.");
      return;
    }

    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    // Read token from stdin (for security)
    writeln("Enter token for ", identity.name, " (input hidden):");
    write("> ");

    // In a full implementation, we would disable echo
    // For now, read from stdin
    var token: string;
    if !stdin.readLine(token) {
      printError("Failed to read token");
      return;
    }
    token = token.strip();

    if token == "" {
      printError("Token cannot be empty");
      return;
    }

    const result = Keychain.storeToken(providerToString(identity.provider), identity.name, identity.user, token);
    if result {
      printSuccess("Token stored in keychain");
    } else {
      printError("Failed to store token");
    }
  }

  proc handleTokenGet(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler token get <identity>");
      return;
    }

    const name = args[0];
    printDebug("Getting token for: " + name);

    if !Keychain.isDarwin() {
      printError("Keychain integration requires macOS");
      return;
    }

    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    const (found, token) = Keychain.retrieveToken(providerToString(identity.provider), identity.name, identity.user);
    if found {
      // Mask the token for display
      const masked = if token.size > 8
                     then token[0..3] + "..." + token[token.size-4..]
                     else "****";
      writeln(bold("Token: "), masked);
      writeln(dim("(Use 'security find-generic-password' for full token)"));
    } else {
      printWarning("No token found for " + name);
    }
  }

  proc handleTokenClear(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler token clear <identity>");
      return;
    }

    const name = args[0];
    printDebug("Clearing token for: " + name);

    if !Keychain.isDarwin() {
      printError("Keychain integration requires macOS");
      return;
    }

    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    const result = Keychain.deleteToken(providerToString(identity.provider), identity.name, identity.user);
    if result {
      printSuccess("Token removed from keychain");
    } else {
      printWarning("No token found to remove");
    }
  }

  proc handleTokenVerify() {
    printDebug("Verifying all tokens");

    writeln(bold("Verifying credentials..."));
    writeln();

    const identities = Identity.listIdentities();

    for identity in identities {
      write("  ", identity.name, "... ");

      const (hasToken, _) = ProviderCLI.resolveCredential(identity);
      if hasToken {
        writeln(green("OK"));

        // Also check for expiry warnings
        const healthResult = TokenHealth.checkTokenHealth(identity);
        if healthResult.needsRenewal {
          writeln("    ", yellow("[WARNING] Expires in " + healthResult.daysUntilExpiry:string + " days"));
        } else if healthResult.isExpired {
          writeln("    ", red("[EXPIRED]"));
        }
      } else {
        writeln(yellow("Not found"));
      }
    }
  }

  proc handleTokenCheckExpiry(args: list(string)) {
    printDebug("Checking token expiry");

    const identities = Identity.listIdentities();

    if args.size > 0 {
      // Check specific identity
      const name = args[0];
      const identity = GlobalConfig.getIdentity(name);

      if identity.name == "" {
        printError("Identity not found: " + name);
        return;
      }

      writeln(bold("Token Health: "), identity.name);
      writeln();

      const healthResult = TokenHealth.checkTokenHealth(identity);
      write(TokenHealth.formatHealthResult(healthResult));

    } else {
      // Check all identities
      TokenHealth.printTokenHealthSummary(identities);
    }
  }

  proc handleTokenRenew(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler token renew <identity>");
      return;
    }

    const name = args[0];
    printDebug("Renewing token for: " + name);

    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    // Show current token health
    writeln(bold("Current Token Status:"));
    writeln();
    const healthResult = TokenHealth.checkTokenHealth(identity);
    write(TokenHealth.formatHealthResult(healthResult));
    writeln();

    // Prompt for renewal
    const renewed = TokenHealth.renewToken(identity);
    if renewed {
      printSuccess("Token renewed successfully");
    } else {
      printError("Token renewal failed or cancelled");
    }
  }

  // Handle 'gpg' subcommands
  proc handleGPG(args: list(string)) {
    if args.size < 1 {
      // Default to status
      handleGPGStatus();
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "status" do handleGPGStatus();
      when "configure", "config" do handleGPGConfigure(subArgs);
      when "verify", "check" do handleGPGVerify();
      otherwise {
        printError("Unknown gpg subcommand: " + subcommand);
        writeln("Available: status, configure, verify");
      }
    }
  }

  proc handleGPGStatus() {
    printDebug("Showing GPG status");

    writeln(bold("GPG Configuration:"));
    writeln();

    // List available keys
    const keys = GPG.listKeys();

    if keys.size == 0 {
      writeln(dim("No GPG secret keys found"));
      writeln();
      writeln("Generate a key with: gpg --full-generate-key");
      return;
    }

    writeln(bold("Available Keys:"));
    for key in keys {
      writeln("  ", bold(key.keyId));
      writeln("    Name:   ", key.name);
      writeln("    Email:  ", key.email);
      if key.expires != "" {
        writeln("    Expires: ", key.expires);
      }
      writeln();
    }

    // Show identity GPG configuration
    writeln(bold("Identity GPG Settings:"));
    const identities = Identity.listIdentities();

    for identity in identities {
      write("  ", identity.name, ": ");
      if identity.gpg.isConfigured() {
        writeln(identity.gpg.keyId);
        writeln("    Sign commits: ", if identity.gpg.signCommits then green("yes") else dim("no"));
        writeln("    Sign tags:    ", if identity.gpg.signTags then green("yes") else dim("no"));
        writeln("    Auto signoff: ", if identity.gpg.autoSignoff then green("yes") else dim("no"));
      } else {
        writeln(dim("not configured"));
      }
    }
  }

  proc handleGPGConfigure(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler gpg configure <identity>");
      return;
    }

    const name = args[0];
    printDebug("Configuring GPG for: " + name);

    const (exists, identity) = Identity.getIdentity(name);
    if !exists {
      printError("Identity not found: " + name);
      return;
    }

    writeln(bold("Configuring GPG for: "), identity.name);
    writeln();

    // Try to find key by email
    const (found, keyId) = GPG.getKeyForEmail(identity.email);
    if found {
      writeln("Found GPG key for ", identity.email, ": ", bold(keyId));
      writeln();
      writeln("Update the config file to add this key:");
      writeln("  ", GlobalConfig.getConfigPath());
    } else {
      printWarning("No GPG key found for " + identity.email);
      writeln();
      writeln("Generate a key with:");
      writeln("  gpg --full-generate-key");
      writeln();
      writeln("Use the same email: ", identity.email);
    }
  }

  proc handleGPGVerify() {
    printDebug("Verifying GPG keys with providers");

    writeln(bold("Verifying GPG registration..."));
    writeln();

    const identities = Identity.listIdentities();

    for identity in identities {
      if !identity.gpg.isConfigured() {
        continue;
      }

      write("  ", identity.name, " (", identity.gpg.keyId, ")... ");

      const result = GPG.verifyKeyWithProvider(identity);
      if result.verified {
        writeln(green("registered"));
      } else {
        writeln(yellow("not verified"));
        if verbose {
          writeln("    ", result.message);
        }
        writeln("    Add at: ", GPG.getGPGSettingsURL(identity));
      }
    }
  }

  // Handle 'debug' subcommands
  proc handleDebug(args: list(string)) {
    if args.size < 1 {
      printError("Missing subcommand");
      writeln("Usage: remote-juggler debug <ssh-config|git-config|keychain|hsm>");
      return;
    }

    const subcommand = args[0];

    select subcommand {
      when "ssh-config", "ssh" do handleDebugSSH();
      when "git-config", "gitconfig", "git" do handleDebugGit();
      when "keychain" do handleDebugKeychain();
      when "hsm", "tpm", "secure-enclave" do handleDebugHSM();
      otherwise {
        printError("Unknown debug subcommand: " + subcommand);
        writeln("Available: ssh-config, git-config, keychain, hsm");
      }
    }
  }

  proc handleDebugSSH() {
    printDebug("Dumping SSH config");

    writeln(bold("Parsed SSH Configuration:"));
    writeln();

    const sshConfigPath = expandPath("~/.ssh/config");
    writeln("Source: ", sshConfigPath);
    writeln();

    const hosts = Config.parseSSHConfig(sshConfigPath);

    for host in hosts {
      writeln(bold("Host "), host.host);
      writeln("  Hostname:     ", host.hostname);
      writeln("  User:         ", host.user);
      writeln("  IdentityFile: ", host.identityFile);
      if host.port != 22 {
        writeln("  Port:         ", host.port);
      }
      if host.proxyJump != "" {
        writeln("  ProxyJump:    ", host.proxyJump);
      }
      writeln();
    }
  }

  proc handleDebugGit() {
    printDebug("Dumping gitconfig");

    writeln(bold("Parsed Git Configuration:"));
    writeln();

    const gitConfigPath = expandPath("~/.gitconfig");
    writeln("Source: ", gitConfigPath);
    writeln();

    const rewrites = Config.parseGitConfigRewrites(gitConfigPath);

    writeln(bold("URL Rewrites (insteadOf):"));
    for rewrite in rewrites {
      writeln("  ", rewrite.fromURL, " -> ", rewrite.toURL);
    }
    writeln();

    // Also show user config
    writeln(bold("User Configuration:"));
    const userConfig = Config.parseGitUserConfig(gitConfigPath);
    writeln("  name:  ", userConfig.name);
    writeln("  email: ", userConfig.email);
    if userConfig.signingKey != "" {
      writeln("  signingKey: ", userConfig.signingKey);
    }
  }

  proc handleDebugKeychain() {
    printDebug("Testing keychain access");

    writeln(bold("Keychain Status:"));
    writeln();

    if !Keychain.isDarwin() {
      writeln(yellow("Platform: "), "Not macOS - keychain not available");
      return;
    }

    writeln(green("Platform: "), "macOS - keychain available");
    writeln();

    // Test storing and retrieving
    const testProvider = "test";
    const testIdentity = "test-identity";
    const testAccount = "test-account";
    const testToken = "test-token-" + Time.timeSinceEpoch().totalSeconds():string;

    write("  Store test token... ");
    const storeResult = Keychain.storeToken(testProvider, testIdentity, testAccount, testToken);
    if storeResult {
      writeln(green("OK"));
    } else {
      writeln(red("FAILED"));
      return;
    }

    write("  Retrieve test token... ");
    const (retrieveOk, retrieved) = Keychain.retrieveToken(testProvider, testIdentity, testAccount);
    if retrieveOk && retrieved == testToken {
      writeln(green("OK"));
    } else {
      writeln(red("FAILED"));
    }

    write("  Delete test token... ");
    const deleteResult = Keychain.deleteToken(testProvider, testIdentity, testAccount);
    if deleteResult {
      writeln(green("OK"));
    } else {
      writeln(red("FAILED"));
    }
  }

  // Helper to expand ~ in paths
  proc expandPath(path: string): string {
    if path.startsWith("~") {
      const home = Core.getEnvVar("HOME");
      return home + path[1..];
    }
    return path;
  }

  // ==========================================================================
  // Main Entry Point
  // ==========================================================================

  proc main(args: [] string) {
    // Handle server modes first (these don't return)
    if mode == "mcp" {
      printDebug("Starting MCP server");
      MCP.runMCPServer();
      return;
    }

    if mode == "acp" {
      printDebug("Starting ACP server");
      ACP.runACPServer();
      return;
    }

    // CLI mode
    printDebug("CLI mode, args: " + args.size:string);

    // Handle help flag
    if help {
      printUsage();
      return;
    }

    // Handle no arguments (show status)
    if args.size < 2 {
      handleStatus();
      return;
    }

    // Parse command (args[0] is program name, args[1] is command)
    const command = args[1];

    // Convert remaining arguments to list for handler functions
    var subArgs = new list(string);
    for i in 2..<args.size {
      subArgs.pushBack(args[i]);
    }

    // Route to command handler
    select command {
      when "list", "ls" do handleList(subArgs);
      when "detect" do handleDetect(subArgs);
      when "switch", "to" do handleSwitch(subArgs);
      when "validate", "test" do handleValidate(subArgs);
      when "verify" do handleVerify(subArgs);
      when "status" do handleStatus();
      when "config" do handleConfig(subArgs);
      when "token" do handleToken(subArgs);
      when "gpg" do handleGPG(subArgs);
      when "debug" do handleDebug(subArgs);
      when "pin" do handlePin(subArgs);
      when "security-mode" do handleSecurityMode(subArgs);
      when "yubikey", "yk" do handleYubiKey(subArgs);
      when "trusted-workstation", "tws" do handleTrustedWorkstationCmd(subArgs);
      when "keys", "kdbx" do handleKeys(subArgs);
      when "setup" do handleSetup(subArgs);
      when "unseal-pin" do handleUnsealPin(subArgs);
      when "help", "--help", "-h" do printUsage();
      when "version", "--version", "-v" {
        writeln("RemoteJuggler v", Core.VERSION);
      }
      otherwise {
        printError("Unknown command: " + command);
        writeln();
        writeln("Run 'remote-juggler help' for usage information.");
      }
    }
  }

  // ==========================================================================
  // PIN Management Command Handlers (Trusted Workstation Mode)
  // ==========================================================================

  // Handle 'pin' subcommands
  proc handlePin(args: list(string)) {
    if args.size < 1 {
      printError("Missing subcommand");
      writeln("Usage: remote-juggler pin <store|clear|status> [identity]");
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "store" do handlePinStore(subArgs);
      when "clear", "delete", "rm" do handlePinClear(subArgs);
      when "status", "check" do handlePinStatus(subArgs);
      otherwise {
        printError("Unknown pin subcommand: " + subcommand);
        writeln("Available: store, clear, status");
      }
    }
  }

  // HSM functions are already available via public use HSM above

  proc handlePinStore(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler pin store <identity>");
      return;
    }

    const name = args[0];
    printDebug("Storing PIN for: " + name);

    // Check if HSM is available
    const hsmType = hsm_detect_available();
    if hsmType == HSM_TYPE_NONE {
      printError("No HSM backend available");
      writeln("Trusted Workstation mode requires:");
      writeln("  - Linux: TPM 2.0 (/dev/tpmrm0)");
      writeln("  - macOS: Secure Enclave (T1/T2/M1+)");
      return;
    }

    const hsmTypeName = hsm_type_name(hsmType);
    printInfo("Using HSM backend: " + hsmTypeName);

    // Verify identity exists
    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    // Read PIN from stdin
    writeln("Enter YubiKey PIN for ", green(identity.name), " (input hidden):");
    write("> ");

    var pin: string;
    if !stdin.readLine(pin) {
      printError("Failed to read PIN");
      return;
    }
    pin = pin.strip();

    if pin == "" {
      printError("PIN cannot be empty");
      return;
    }

    if pin.size < 6 || pin.size > 127 {
      printError("PIN must be between 6 and 127 characters");
      return;
    }

    // Store PIN in HSM
    const result = hsm_store_pin(name, pin, pin.size);

    if result == HSM_SUCCESS {
      printSuccess("PIN stored securely in " + hsmTypeName);
      writeln();
      writeln("The PIN is now sealed and can only be retrieved on this device");
      writeln("under the same security conditions.");
      writeln();
      writeln("To enable trusted workstation mode, run:");
      writeln("  remote-juggler security-mode trusted_workstation");
    } else {
      const errMsg = hsm_error_message(result);
      printError("Failed to store PIN: " + errMsg);
    }
  }

  proc handlePinClear(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler pin clear <identity>");
      return;
    }

    const name = args[0];
    printDebug("Clearing PIN for: " + name);

    // Check if HSM is available
    if hsm_is_available() == 0 {
      printError("No HSM backend available");
      return;
    }

    // Check if PIN exists
    if hsm_has_pin(name) == 0 {
      printWarning("No PIN stored for identity: " + name);
      return;
    }

    // Clear the PIN
    const result = hsm_clear_pin(name);

    if result == HSM_SUCCESS {
      printSuccess("PIN cleared from HSM for identity: " + name);
      writeln();
      writeln("To store a new PIN, run:");
      writeln("  remote-juggler pin store ", name);
    } else {
      const errMsg = hsm_error_message(result);
      printError("Failed to clear PIN: " + errMsg);
    }
  }

  proc handlePinStatus(args: list(string)) {
    printDebug("Checking PIN status");

    writeln(bold("PIN Storage Status"));
    writeln("==================");
    writeln();

    // Check HSM availability
    const hsmType = hsm_detect_available();
    write("HSM Backend: ");
    if hsmType != HSM_TYPE_NONE {
      const hsmTypeName = hsm_type_name(hsmType);
      writeln(green(hsmTypeName));
    } else {
      writeln(red("None available"));
      writeln();
      writeln(dim("Trusted Workstation mode requires TPM 2.0 (Linux) or Secure Enclave (macOS)"));
      return;
    }

    writeln();

    // If specific identity requested
    if args.size > 0 {
      const name = args[0];
      const hasPIN = hsm_has_pin(name) != 0;
      writeln("Identity: ", bold(name));
      write("  PIN Stored: ");
      if hasPIN {
        writeln(green("Yes"));
      } else {
        writeln(dim("No"));
      }
      return;
    }

    // Show status for all identities
    writeln("Stored PINs by Identity:");
    const identities = Identity.listIdentities();
    var anyStored = false;

    for identity in identities {
      const hasPIN = hsm_has_pin(identity.name) != 0;
      write("  ", identity.name, ": ");
      if hasPIN {
        writeln(green("stored"));
        anyStored = true;
      } else {
        writeln(dim("-"));
      }
    }

    if !anyStored {
      writeln();
      writeln(dim("No PINs stored. Use 'remote-juggler pin store <identity>' to store one."));
    }

    // Show current security mode
    writeln();
    const settings = GlobalConfig.loadSettings();
    writeln("Current Security Mode: ", bold(settings.defaultSecurityMode));

    if settings.defaultSecurityMode == "trusted_workstation" {
      writeln(green("  Trusted Workstation mode is active"));
      writeln("  YubiKey PINs will be retrieved from HSM automatically");
    } else {
      writeln(dim("  Run 'remote-juggler security-mode trusted_workstation' to enable"));
    }
  }

  // Handle 'security-mode' command
  proc handleSecurityMode(args: list(string)) {
    if args.size < 1 {
      // Show current mode
      const settings = GlobalConfig.loadSettings();
      writeln(bold("Current Security Mode: "), settings.defaultSecurityMode);
      writeln();
      writeln("Available modes:");
      writeln("  ", bold("maximum_security"), " - PIN required for every signing operation");
      writeln("  ", bold("developer_workflow"), " - PIN cached by gpg-agent (default)");
      writeln("  ", bold("trusted_workstation"), " - PIN stored in HSM, auto-retrieved");
      writeln();
      writeln("Change mode with: remote-juggler security-mode <mode>");
      return;
    }

    const mode = args[0];

    // Validate mode
    if mode != "maximum_security" && mode != "developer_workflow" && mode != "trusted_workstation" {
      printError("Invalid security mode: " + mode);
      writeln("Valid modes: maximum_security, developer_workflow, trusted_workstation");
      return;
    }

    // Check HSM requirement for trusted_workstation
    if mode == "trusted_workstation" {
      if hsm_is_available() == 0 {
        printError("Cannot enable trusted_workstation mode: No HSM available");
        writeln("Trusted Workstation mode requires:");
        writeln("  - Linux: TPM 2.0 (/dev/tpmrm0)");
        writeln("  - macOS: Secure Enclave (T1/T2/M1+)");
        return;
      }
    }

    // Update settings
    const success = GlobalConfig.setSecurityMode(mode);

    if success {
      printSuccess("Security mode set to: " + mode);
      writeln();

      select mode {
        when "maximum_security" {
          writeln("YubiKey PIN will be required for every signing operation.");
          writeln("This provides maximum security but requires manual PIN entry.");
        }
        when "developer_workflow" {
          writeln("PIN will be cached by gpg-agent for the session.");
          writeln("Enter PIN once, then sign multiple times without re-entry.");
        }
        when "trusted_workstation" {
          writeln("PIN will be retrieved from HSM (TPM/SecureEnclave) automatically.");
          writeln("No PIN entry required on this trusted device.");
          writeln();
          writeln(yellow("IMPORTANT:"), " Switching away from this mode will require");
          writeln("re-entering the PIN. The HSM-stored PIN is not transferable.");
          writeln();
          writeln("Ensure PINs are stored: remote-juggler pin status");
        }
      }
    } else {
      printError("Failed to set security mode");
    }
  }

  // Handle 'debug hsm' subcommand
  proc handleDebugHSM() {
    printDebug("Testing HSM access");

    writeln(bold("HSM (Hardware Security Module) Status:"));
    writeln();

    // Platform detection
    const hsmType = hsm_detect_available();
    write("Detected HSM: ");

    select hsmType {
      when HSM_TYPE_TPM {
        writeln(green("TPM 2.0"));
        writeln("  Device: /dev/tpmrm0 (Linux Resource Manager)");
        writeln("  Security: PIN sealed to PCR 7 (Secure Boot state)");
      }
      when HSM_TYPE_SECURE_ENCLAVE {
        writeln(green("Apple Secure Enclave"));
        writeln("  Hardware: T1/T2/Apple Silicon");
        writeln("  Security: ECIES encryption with biometric/password");
      }
      when HSM_TYPE_KEYCHAIN {
        writeln(yellow("Keychain (Fallback)"));
        writeln("  Platform: System credential store");
        writeln("  Security: Protected by login password (less secure)");
      }
      otherwise {
        writeln(red("None"));
        writeln("  No HSM backend available on this system");
        return;
      }
    }

    writeln();

    // Test store/retrieve cycle with a test identity
    const testIdentity = "__test_hsm__";
    const testPin = "test123456";

    write("Testing HSM operations... ");

    // Store
    var storeResult = hsm_store_pin(testIdentity, testPin, testPin.size);
    if storeResult != HSM_SUCCESS {
      const errMsg = hsm_error_message(storeResult);
      writeln(red("FAILED"));
      writeln("  Store failed: ", errMsg);
      return;
    }

    // Retrieve
    var (retrieveResult, retrievedPin) = hsm_retrieve_pin(testIdentity);

    if retrieveResult != HSM_SUCCESS {
      const errMsg = hsm_error_message(retrieveResult);
      writeln(red("FAILED"));
      writeln("  Retrieve failed: ", errMsg);
      hsm_clear_pin(testIdentity);
      return;
    }

    // Verify
    var match = (retrievedPin == testPin);

    // Clean up test identity
    hsm_clear_pin(testIdentity);

    if match {
      writeln(green("OK"));
      writeln();
      writeln("HSM is working correctly. Trusted Workstation mode is available.");
    } else {
      writeln(red("FAILED"));
      writeln("  Retrieved PIN does not match stored PIN");
    }
  }

  // ==========================================================================
  // YubiKey Management Command Handlers
  // ==========================================================================

  // Handle 'yubikey' subcommands
  proc handleYubiKey(args: list(string)) {
    if args.size < 1 {
      // Default to info
      handleYubiKeyInfo();
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "info", "status" do handleYubiKeyInfo();
      when "set-pin-policy", "pin-policy" do handleYubiKeySetPinPolicy(subArgs);
      when "set-touch", "touch" do handleYubiKeySetTouch(subArgs);
      when "configure-trusted", "trusted" do handleYubiKeyConfigureTrusted();
      when "diagnostics", "diag", "check" do handleYubiKeyDiagnostics();
      otherwise {
        printError("Unknown yubikey subcommand: " + subcommand);
        writeln("Available: info, set-pin-policy, set-touch, configure-trusted, diagnostics");
      }
    }
  }

  proc handleYubiKeyInfo() {
    printDebug("Showing YubiKey info");

    writeln(bold("YubiKey Status"));
    writeln("==============");
    writeln();

    // Check ykman availability
    if !YubiKey.isYkmanAvailable() {
      printError("ykman (YubiKey Manager) is not installed");
      writeln();
      writeln("Install ykman to manage YubiKey settings:");
      writeln("  pip install yubikey-manager");
      writeln("  brew install ykman          # macOS");
      writeln("  apt install yubikey-manager # Debian/Ubuntu");
      return;
    }

    const ykmanVersion = YubiKey.getYkmanVersion();
    writeln("ykman version: ", green(ykmanVersion));
    writeln();

    // Check YubiKey connection
    if !YubiKey.isYubiKeyConnected() {
      printWarning("No YubiKey detected");
      writeln();
      writeln("Insert a YubiKey to view device information.");
      return;
    }

    // Get comprehensive info
    const info = YubiKey.getYubiKeyInfo();

    writeln(bold("Device Information:"));
    writeln("  Serial Number: ", green(info.serialNumber));
    if info.firmware != "" {
      writeln("  Firmware:      ", info.firmware);
    }
    if info.formFactor != "" {
      writeln("  Form Factor:   ", info.formFactor);
    }

    writeln();
    writeln(bold("OpenPGP Configuration:"));

    // PIN policy
    write("  Signature PIN Policy: ");
    if info.sigPinPolicy == "once" {
      writeln(green("once"), " (PIN cached for session)");
    } else if info.sigPinPolicy == "always" {
      writeln(yellow("always"), " (PIN required every time)");
    } else {
      writeln(dim("unknown"));
    }

    // Touch policies
    writeln();
    writeln("  Touch Policies:");
    writeln("    Signature:      ", formatTouchPolicy(info.sigTouchPolicy));
    writeln("    Encryption:     ", formatTouchPolicy(info.encTouchPolicy));
    writeln("    Authentication: ", formatTouchPolicy(info.autTouchPolicy));

    // Key slots
    writeln();
    writeln("  Key Slots:");
    writeln("    Signature:      ", if info.sigKeyPresent then green("Present") else dim("Empty"));
    writeln("    Encryption:     ", if info.encKeyPresent then green("Present") else dim("Empty"));
    writeln("    Authentication: ", if info.autKeyPresent then green("Present") else dim("Empty"));

    // PIN retries
    if info.pinRetries > 0 {
      writeln();
      write("  PIN Retries: ");
      if info.pinRetries <= 2 {
        writeln(yellow(info.pinRetries:string), "/", info.resetRetries:string, "/", info.adminRetries:string);
        writeln("    ", yellow("[WARNING]"), " Low PIN retries remaining!");
      } else {
        writeln(info.pinRetries:string, "/", info.resetRetries:string, "/", info.adminRetries:string);
      }
    }

    // Trusted Workstation readiness
    writeln();
    write(bold("Trusted Workstation Ready: "));
    if info.isTrustedWorkstationReady() {
      writeln(green("Yes"));
      writeln("  YubiKey is configured for optimal automation.");
    } else {
      writeln(yellow("No"));
      writeln("  Recommendations for Trusted Workstation mode:");
      if info.sigPinPolicy == "always" {
        writeln("    - Run: remote-juggler yubikey set-pin-policy once");
      }
      if info.sigTouchPolicy == "on" || info.sigTouchPolicy == "fixed" {
        writeln("    - Run: remote-juggler yubikey set-touch sig cached");
      }
      writeln();
      writeln("  Or run: remote-juggler yubikey configure-trusted");
    }
  }

  // Helper to format touch policy with colors
  proc formatTouchPolicy(policy: string): string {
    select policy {
      when "off" do return green("off") + " (no touch required)";
      when "cached" do return green("cached") + " (touch cached 15s)";
      when "on" do return yellow("on") + " (touch every time)";
      when "fixed" do return red("fixed") + " (cannot be changed)";
      otherwise do return dim("unknown");
    }
  }

  proc handleYubiKeySetPinPolicy(args: list(string)) {
    if args.size < 1 {
      printError("Missing policy argument");
      writeln("Usage: remote-juggler yubikey set-pin-policy <once|always>");
      writeln();
      writeln("Policies:");
      writeln("  once   - PIN required once per session (recommended)");
      writeln("  always - PIN required for every signature");
      return;
    }

    const policy = args[0];
    printDebug("Setting PIN policy to: " + policy);

    if policy != "once" && policy != "always" {
      printError("Invalid policy: " + policy);
      writeln("Valid policies: once, always");
      return;
    }

    // Check prerequisites
    if !YubiKey.isYkmanAvailable() {
      printError("ykman is not installed");
      return;
    }

    if !YubiKey.isYubiKeyConnected() {
      printError("No YubiKey connected");
      return;
    }

    writeln("Setting signature PIN policy to '", bold(policy), "'...");
    writeln();
    writeln(yellow("Note:"), " This operation requires the admin PIN.");
    writeln();

    const success = YubiKey.setSignaturePinPolicy(policy);

    if success {
      printSuccess("PIN policy set to '" + policy + "'");

      if policy == "once" {
        writeln();
        writeln("The YubiKey will now cache the PIN for the session.");
        writeln("Enter PIN once, then sign multiple times without re-entry.");
      }
    } else {
      printError("Failed to set PIN policy");
      writeln();
      writeln("Possible causes:");
      writeln("  - Admin PIN was incorrect");
      writeln("  - YubiKey firmware doesn't support this feature");
      writeln("  - YubiKey is in a locked state");
    }
  }

  proc handleYubiKeySetTouch(args: list(string)) {
    if args.size < 2 {
      printError("Missing arguments");
      writeln("Usage: remote-juggler yubikey set-touch <slot> <policy>");
      writeln();
      writeln("Slots:");
      writeln("  sig - Signature key");
      writeln("  enc - Encryption key");
      writeln("  aut - Authentication key");
      writeln();
      writeln("Policies:");
      writeln("  on     - Touch required for every operation");
      writeln("  off    - Touch never required");
      writeln("  cached - Touch cached for 15 seconds (recommended)");
      return;
    }

    const slot = args[0];
    const policy = args[1];
    printDebug("Setting touch policy for " + slot + " to: " + policy);

    // Validate slot
    if slot != "sig" && slot != "enc" && slot != "aut" {
      printError("Invalid slot: " + slot);
      writeln("Valid slots: sig, enc, aut");
      return;
    }

    // Validate policy
    if policy != "on" && policy != "off" && policy != "cached" {
      printError("Invalid policy: " + policy);
      writeln("Valid policies: on, off, cached");
      return;
    }

    // Check prerequisites
    if !YubiKey.isYkmanAvailable() {
      printError("ykman is not installed");
      return;
    }

    if !YubiKey.isYubiKeyConnected() {
      printError("No YubiKey connected");
      return;
    }

    // Get current info to check if slot has a key
    const info = YubiKey.getYubiKeyInfo();
    var hasKey = false;
    select slot {
      when "sig" do hasKey = info.sigKeyPresent;
      when "enc" do hasKey = info.encKeyPresent;
      when "aut" do hasKey = info.autKeyPresent;
    }

    if !hasKey {
      printWarning("No key present in " + slot + " slot");
      writeln("Touch policy only applies when a key is loaded.");
      writeln();
    }

    writeln("Setting touch policy for '", bold(slot), "' to '", bold(policy), "'...");
    writeln();
    writeln(yellow("Note:"), " This operation requires the admin PIN.");
    writeln();

    const success = YubiKey.setTouchPolicy(slot, policy);

    if success {
      printSuccess("Touch policy for '" + slot + "' set to '" + policy + "'");

      select policy {
        when "cached" {
          writeln();
          writeln("Touch will be required once, then cached for 15 seconds.");
          writeln("This allows multiple operations without repeated touches.");
        }
        when "off" {
          writeln();
          writeln(yellow("Warning:"), " Disabling touch removes a security layer.");
          writeln("Consider using 'cached' for a balance of security and convenience.");
        }
      }
    } else {
      printError("Failed to set touch policy");
      writeln();
      writeln("Possible causes:");
      writeln("  - Admin PIN was incorrect");
      writeln("  - Touch policy is 'fixed' and cannot be changed");
      writeln("  - YubiKey firmware doesn't support this feature");
    }
  }

  proc handleYubiKeyConfigureTrusted() {
    printDebug("Configuring YubiKey for Trusted Workstation mode");

    writeln(bold("Configuring YubiKey for Trusted Workstation Mode"));
    writeln("=================================================");
    writeln();

    // Check prerequisites
    if !YubiKey.isYkmanAvailable() {
      printError("ykman is not installed");
      return;
    }

    if !YubiKey.isYubiKeyConnected() {
      printError("No YubiKey connected");
      return;
    }

    // Get current info
    const infoBefore = YubiKey.getYubiKeyInfo();

    writeln("Current configuration:");
    writeln("  PIN Policy:   ", if infoBefore.sigPinPolicy != "" then infoBefore.sigPinPolicy else "unknown");
    writeln("  Touch Policy: ", if infoBefore.sigTouchPolicy != "" then infoBefore.sigTouchPolicy else "unknown");
    writeln();

    if infoBefore.isTrustedWorkstationReady() {
      printInfo("YubiKey is already configured for Trusted Workstation mode");
      return;
    }

    writeln("This will configure:");
    writeln("  - PIN Policy:   once (PIN cached for session)");
    writeln("  - Touch Policy: cached (touch cached for 15 seconds)");
    writeln();
    writeln(yellow("Note:"), " This operation requires the admin PIN.");
    writeln();

    // Perform configuration
    const (success, message) = YubiKey.configureForTrustedWorkstation();

    writeln();
    if success {
      printSuccess("YubiKey configured for Trusted Workstation mode");
      writeln();
      writeln("Configuration details: ", message);
      writeln();
      writeln("Next steps:");
      writeln("  1. Store your PIN securely: remote-juggler pin store <identity>");
      writeln("  2. Enable Trusted Workstation mode: remote-juggler security-mode trusted_workstation");
    } else {
      printError("Configuration partially failed");
      writeln("Details: ", message);
      writeln();
      writeln("Try configuring individual settings:");
      writeln("  remote-juggler yubikey set-pin-policy once");
      writeln("  remote-juggler yubikey set-touch sig cached");
    }
  }

  proc handleYubiKeyDiagnostics() {
    printDebug("Running YubiKey diagnostics");

    writeln(bold("YubiKey Diagnostics"));
    writeln("===================");
    writeln();

    const results = YubiKey.runDiagnostics();

    for result in results {
      if result.startsWith("[OK]") {
        writeln(green("[OK]"), result[4..]);
      } else if result.startsWith("[FAIL]") {
        writeln(red("[FAIL]"), result[6..]);
      } else if result.startsWith("[WARN]") {
        writeln(yellow("[WARN]"), result[6..]);
      } else if result.startsWith("[INFO]") {
        writeln(cyan("[INFO]"), result[6..]);
      } else {
        writeln(result);
      }
    }

    writeln();

    // Check if ykman is available for additional recommendations
    if YubiKey.isYkmanAvailable() && YubiKey.isYubiKeyConnected() {
      const info = YubiKey.getYubiKeyInfo();

      if info.isTrustedWorkstationReady() {
        writeln(bold("Summary: "), green("YubiKey is ready for Trusted Workstation mode"));
      } else {
        writeln(bold("Summary: "), yellow("YubiKey needs configuration for optimal automation"));
        writeln();
        writeln("Run 'remote-juggler yubikey configure-trusted' to configure automatically.");
      }
    }
  }

  // ==========================================================================
  // Trusted Workstation Orchestration Command Handlers
  // ==========================================================================

  // Handle 'trusted-workstation' subcommands
  proc handleTrustedWorkstationCmd(args: list(string)) {
    if args.size < 1 {
      // Default to status
      handleTrustedWorkstationStatus(emptyArgs());
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "enable" do handleTrustedWorkstationEnable(subArgs);
      when "disable" do handleTrustedWorkstationDisable(subArgs);
      when "status" do handleTrustedWorkstationStatus(subArgs);
      when "verify" do handleTrustedWorkstationVerify(subArgs);
      otherwise {
        printError("Unknown trusted-workstation subcommand: " + subcommand);
        writeln("Available: enable, disable, status, verify");
      }
    }
  }

  proc handleTrustedWorkstationEnable(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler trusted-workstation enable <identity>");
      return;
    }

    const name = args[0];
    printDebug("Enabling Trusted Workstation for: " + name);

    // Check if identity exists
    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    writeln(bold("Enabling Trusted Workstation Mode"));
    writeln("==================================");
    writeln();
    writeln("Identity: ", green(name));
    writeln();

    // Check prerequisites
    writeln("Checking prerequisites...");
    const prereqs = TrustedWorkstation.checkPrerequisites(name);

    if !prereqs.allMet {
      writeln();
      printError("Prerequisites not met:");
      for issue in prereqs.issues {
        writeln("  - ", red(issue));
      }
      return;
    }

    // Show warnings
    for warning in prereqs.warnings {
      printWarning(warning);
    }

    writeln(green("[OK]"), " All prerequisites met");
    writeln();

    // Prompt for PIN
    writeln("Enter YubiKey PIN for ", green(name), " (input hidden):");
    write("> ");

    var pin: string;
    if !stdin.readLine(pin) {
      printError("Failed to read PIN");
      return;
    }
    pin = pin.strip();

    if pin == "" {
      printError("PIN cannot be empty");
      return;
    }

    writeln();
    writeln("Enabling Trusted Workstation mode...");
    writeln();

    // Use the TrustedWorkstation module to enable
    const result = TrustedWorkstation.enableTrustedWorkstation(name, pin);

    // Clear PIN from memory
    pin = "";

    if result.success {
      printSuccess(result.message);
      writeln();
      writeln("Trusted Workstation mode is now active.");
      writeln();
      writeln("What this means:");
      writeln("  - YubiKey PIN is stored securely in HSM (TPM/SecureEnclave)");
      writeln("  - gpg-agent is configured to use custom pinentry");
      writeln("  - PIN will be auto-retrieved during signing operations");
      writeln("  - YubiKey PIN policy set to 'once' (if ykman available)");
      writeln();
      writeln("To verify: remote-juggler trusted-workstation verify ", name);
      writeln("To disable: remote-juggler trusted-workstation disable ", name);
    } else {
      printError("Failed to enable Trusted Workstation mode");
      writeln("Error: ", result.message);
    }
  }

  proc handleTrustedWorkstationDisable(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler trusted-workstation disable <identity>");
      return;
    }

    const name = args[0];
    printDebug("Disabling Trusted Workstation for: " + name);

    // Check if identity exists
    const identity = GlobalConfig.getIdentity(name);
    if identity.name == "" {
      printError("Identity not found: " + name);
      return;
    }

    writeln(bold("Disabling Trusted Workstation Mode"));
    writeln("===================================");
    writeln();
    writeln("Identity: ", yellow(name));
    writeln();

    // Confirm action
    writeln(yellow("Warning:"), " This will:");
    writeln("  - Remove PIN from HSM storage");
    writeln("  - Restore original gpg-agent configuration");
    writeln("  - Reset YubiKey PIN policy to 'always' (if ykman available)");
    writeln();
    writeln("You will need to re-enter your PIN for signing operations.");
    writeln();

    // Disable
    const result = TrustedWorkstation.disableTrustedWorkstation(name);

    if result.success {
      printSuccess(result.message);
      writeln();
      writeln("Trusted Workstation mode has been disabled.");
      writeln("You will be prompted for your PIN during signing operations.");
    } else {
      printError("Failed to disable Trusted Workstation mode");
      writeln("Error: ", result.message);
    }
  }

  proc handleTrustedWorkstationStatus(args: list(string)) {
    printDebug("Checking Trusted Workstation status");

    const identity = if args.size > 0 then args[0] else "";
    const status = TrustedWorkstation.getTrustedWorkstationStatus(identity);

    writeln(bold("Trusted Workstation Status"));
    writeln("===========================");
    writeln();

    // Mode status
    write("Mode: ");
    if status.enabled {
      writeln(green("ENABLED"));
    } else {
      writeln(dim("DISABLED"));
    }

    if status.identity != "" {
      writeln("Identity: ", bold(status.identity));
    }

    writeln();

    // HSM status
    writeln(bold("Hardware Security Module:"));
    write("  Available: ");
    if status.hsmAvailable {
      writeln(green("Yes"), " (", status.hsmType, ")");
    } else {
      writeln(red("No"));
    }

    write("  PIN Stored: ");
    if status.pinStored {
      writeln(green("Yes"));
    } else {
      writeln(dim("No"));
    }

    writeln();

    // gpg-agent status
    writeln(bold("GPG Agent:"));
    write("  Running: ");
    if status.gpgAgentRunning {
      writeln(green("Yes"));
    } else {
      writeln(red("No"));
    }

    write("  Custom Pinentry: ");
    if status.pinentryConfigured {
      writeln(green("Configured"));
    } else {
      writeln(dim("Not configured"));
    }

    if status.gpgAgentConfigured {
      writeln("  Pinentry Path: ", dim(status.pinentryPath));
    }

    writeln();

    // YubiKey status
    writeln(bold("YubiKey:"));
    write("  ykman Available: ");
    if status.ykmanAvailable {
      writeln(green("Yes"));
    } else {
      writeln(yellow("No"), " (optional)");
    }

    if status.ykmanAvailable {
      write("  YubiKey Connected: ");
      if status.yubiKeyConnected {
        writeln(green("Yes"));
        if status.yubiKeyPinPolicy != "" {
          writeln("  PIN Policy: ", bold(status.yubiKeyPinPolicy));
        }
      } else {
        writeln(yellow("No"));
      }
    }

    // Show missing prerequisites
    const missing = status.getMissingPrerequisites();
    if missing.size > 0 {
      writeln();
      writeln(yellow("Issues:"));
      for issue in missing {
        writeln("  - ", issue);
      }
    }

    // Recommendations
    if !status.enabled && status.hasPrerequisites() {
      writeln();
      writeln("To enable Trusted Workstation mode:");
      writeln("  remote-juggler trusted-workstation enable <identity>");
    }
  }

  proc handleTrustedWorkstationVerify(args: list(string)) {
    if args.size < 1 {
      printError("Missing identity name");
      writeln("Usage: remote-juggler trusted-workstation verify <identity>");
      return;
    }

    const name = args[0];
    printDebug("Verifying Trusted Workstation for: " + name);

    writeln(bold("Verifying Trusted Workstation"));
    writeln("==============================");
    writeln();
    writeln("Identity: ", green(name));
    writeln();

    writeln("Testing GPG signing...");
    const result = TrustedWorkstation.verifyTrustedWorkstation(name);

    writeln();
    if result.success {
      printSuccess(result.message);
      writeln();
      writeln("Trusted Workstation is working correctly.");
      writeln("GPG signing operations will automatically retrieve the PIN.");
    } else {
      printError("Verification failed");
      writeln("Error: ", result.message);
      writeln();
      writeln("Troubleshooting:");
      writeln("  - Check YubiKey is connected");
      writeln("  - Verify PIN is stored: remote-juggler pin status ", name);
      writeln("  - Check gpg-agent: remote-juggler trusted-workstation status");
    }
  }

  // ==========================================================================
  // Setup Command Handler
  // ==========================================================================

  // Handle 'setup' command
  proc handleSetup(args: list(string)) {
    var mode = Setup.SetupMode.Interactive;

    // Parse setup mode from arguments
    if args.size > 0 {
      mode = Setup.parseSetupMode(args[0]);
    }

    const result = Setup.runSetup(mode);

    if !result.success {
      printError(result.message);
      if result.warnings.size > 0 {
        writeln();
        writeln("Warnings:");
        for warning in result.warnings {
          printWarning(warning);
        }
      }
    }
  }

  // ==========================================================================
  // Key Store (KeePassXC) Command Handlers
  // ==========================================================================

  // Handle 'keys' subcommands
  proc handleKeys(args: list(string)) {
    if args.size < 1 {
      // Default to status
      handleKeysStatus();
      return;
    }

    const subcommand = args[0];
    const subArgs = if args.size > 1 then sublist(args, 1) else new list(string);

    select subcommand {
      when "init" do handleKeysInit();
      when "status" do handleKeysStatus();
      when "search", "find" do handleKeysSearch(subArgs);
      when "resolve" do handleKeysResolve(subArgs);
      when "get" do handleKeysGet(subArgs);
      when "store", "set", "add" do handleKeysStore(subArgs);
      when "delete", "rm" do handleKeysDelete(subArgs);
      when "list", "ls" do handleKeysList(subArgs);
      when "ingest", "import" do handleKeysIngest(subArgs);
      when "crawl" do handleKeysCrawl(subArgs);
      when "discover" do handleKeysDiscover(subArgs);
      when "export", "dump-env" do handleKeysExport(subArgs);
      when "sops-ingest" do handleKeysSopsIngest(subArgs);
      when "sops-sync" do handleKeysSopsSync(subArgs);
      when "sops-export" do handleKeysSopsExport(subArgs);
      otherwise {
        printError("Unknown keys subcommand: " + subcommand);
        writeln("Available: init, status, search, resolve, get, store, delete, list, ingest, crawl, discover, export, sops-ingest, sops-sync, sops-export");
      }
    }
  }

  // Handle 'keys init' - Bootstrap a new kdbx database
  proc handleKeysInit() {
    printDebug("Initializing key store");

    writeln(bold("Initializing KeePassXC Key Store"));
    writeln("==================================");
    writeln();

    // Check keepassxc-cli
    if !KeePassXC.isAvailable() {
      printError("keepassxc-cli not found in PATH");
      writeln();
      writeln("Install KeePassXC to use the key store:");
      writeln("  dnf install keepassxc          # Fedora/RHEL");
      writeln("  apt install keepassxc          # Debian/Ubuntu");
      writeln("  brew install keepassxc         # macOS");
      return;
    }

    writeln(green("[OK]"), " keepassxc-cli found");

    const dbPath = KeePassXC.getDatabasePath();
    writeln("Database path: ", dbPath);
    writeln();

    // Check HSM availability
    const hsmType = hsm_detect_available();
    if hsmType != HSM_TYPE_NONE {
      writeln(green("[OK]"), " HSM available: ", hsm_type_name(hsmType));
    } else {
      printWarning("No HSM available - master password will be shown (store it securely!)");
    }

    // Check YubiKey
    if KeePassXC.isYubiKeyPresent() {
      writeln(green("[OK]"), " YubiKey detected");
    } else {
      printWarning("No YubiKey detected - auto-unlock will require YubiKey presence");
    }

    writeln();
    writeln("Bootstrapping database...");
    writeln();

    const (success, message) = KeePassXC.bootstrapDatabase(dbPath);

    if success {
      printSuccess("Key store initialized");
      writeln();
      writeln(message);
      writeln();

      // Try to import existing credentials
      writeln("Importing existing credentials from environment...");
      if KeePassXC.canAutoUnlock() {
        const (ok, password) = KeePassXC.autoUnlock();
        if ok {
          const imported = KeePassXC.importExistingCredentials(dbPath, password);
          if imported > 0 {
            printSuccess("Imported " + imported:string + " credentials");
          } else {
            writeln(dim("No existing credentials found to import"));
          }
        }
      }

      writeln();
      writeln("Next steps:");
      writeln("  remote-juggler keys status           # Check key store status");
      writeln("  remote-juggler keys ingest ~/path/.env # Ingest .env file");
      writeln("  remote-juggler keys search <query>    # Search entries");
    } else {
      printError("Failed to initialize key store");
      writeln(message);
    }
  }

  // Handle 'keys status' - Show key store status
  proc handleKeysStatus() {
    printDebug("Showing key store status");

    writeln(bold("KeePassXC Key Store Status"));
    writeln("==========================");
    writeln();

    // keepassxc-cli availability
    write("keepassxc-cli: ");
    if KeePassXC.isAvailable() {
      writeln(green("installed"));
    } else {
      writeln(red("NOT FOUND"));
      writeln();
      writeln("Install KeePassXC to use the key store.");
      return;
    }

    // Database
    const dbPath = KeePassXC.getDatabasePath();
    writeln("Database:      ", dbPath);
    write("  Exists:      ");
    if KeePassXC.databaseExists() {
      writeln(green("yes"));
    } else {
      writeln(dim("no"));
      writeln();
      writeln("Run 'remote-juggler keys init' to create a new key store.");
      return;
    }

    // HSM
    const hsmType = hsm_detect_available();
    write("HSM Backend:   ");
    if hsmType != HSM_TYPE_NONE {
      writeln(green(hsm_type_name(hsmType)));
    } else {
      writeln(dim("none"));
    }

    // Master password sealed
    write("Master Sealed: ");
    const hasMaster = hsm_has_pin(KeePassXC.KDBX_HSM_IDENTITY) != 0;
    if hasMaster {
      writeln(green("yes"));
    } else {
      writeln(dim("no"));
    }

    // YubiKey
    write("YubiKey:       ");
    if KeePassXC.isYubiKeyPresent() {
      writeln(green("present"));
    } else {
      writeln(yellow("not detected"));
    }

    // Auto-unlock readiness
    write("Auto-Unlock:   ");
    if KeePassXC.canAutoUnlock() {
      writeln(green("ready"));
    } else {
      writeln(dim("not available"));
    }

    // Entry summary (if accessible)
    if KeePassXC.canAutoUnlock() {
      const (ok, password) = KeePassXC.autoUnlock();
      if ok {
        writeln();
        writeln(bold("Entries:"));
        const (listOk, entries) = KeePassXC.listEntries(dbPath, "RemoteJuggler", password);
        if listOk {
          for entry in entries {
            writeln("  ", entry);
          }
        }
      }
    }
  }

  // Handle 'keys search <query>' - Fuzzy search across all entries
  proc handleKeysSearch(args: list(string)) {
    if args.size < 1 {
      printError("Missing search query");
      writeln("Usage: remote-juggler keys search <query> [--json] [--group <group>]");
      return;
    }

    const query = args[0];
    printDebug("Searching key store for: " + query);

    // Parse flags
    var jsonOutput = false;
    var groupFilter = "";
    for i in 1..<args.size {
      if args[i] == "--json" {
        jsonOutput = true;
      } else if args[i] == "--group" && i + 1 < args.size {
        groupFilter = args[i + 1];
      }
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const results = KeePassXC.search(dbPath, query, password, groupFilter);

    if jsonOutput {
      // Structured JSON output for scripting
      var json = '{"query":"' + query.replace('"', '\\"') + '"';
      if groupFilter != "" then json += ',"group":"' + groupFilter + '"';
      json += ',"count":' + results.size:string + ',"results":[';
      var first = true;
      for result in results {
        if !first then json += ",";
        json += '{"entryPath":"' + result.entryPath.replace('"', '\\"') + '"';
        json += ',"title":"' + result.title.replace('"', '\\"') + '"';
        json += ',"score":' + result.score:string;
        json += ',"matchContext":"' + result.matchContext.replace('"', '\\"') + '"';
        json += ',"matchField":"' + result.matchField + '"}';
        first = false;
      }
      json += "]}";
      writeln(json);
      return;
    }

    if results.size == 0 {
      writeln(dim("No entries found matching '"), query, dim("'"));
      return;
    }

    writeln(bold("Search Results for '"), query, bold("':"));
    writeln();

    for result in results {
      const scoreStr = if result.score >= 100 then green("[exact]")
                       else if result.score >= 70 then green("[substring]")
                       else if result.score >= 60 then cyan("[boundary]")
                       else if result.score >= 40 then cyan("[fuzzy]")
                       else dim("[weak]");

      writeln("  ", scoreStr, " ", bold(result.title));
      writeln("         ", dim(result.entryPath));
      if result.matchField != "path" {
        writeln("         ", dim("(" + result.matchContext + ")"));
      }
    }

    writeln();
    writeln(dim("Found "), results.size:string, dim(" result(s)"));
    writeln(dim("Use 'remote-juggler keys get <path>' to retrieve a secret"));
    writeln(dim("Or  'remote-juggler keys resolve <query>' for one-step search+get"));
  }

  // Handle 'keys resolve <query>' - Search and retrieve in one step
  proc handleKeysResolve(args: list(string)) {
    if args.size < 1 {
      printError("Missing search query");
      writeln("Usage: remote-juggler keys resolve <query> [--group <group>] [--threshold <n>]");
      return;
    }

    const query = args[0];
    printDebug("Resolving key store query: " + query);

    // Parse flags
    var groupFilter = "";
    var threshold = 40;
    for i in 1..<args.size {
      if args[i] == "--group" && i + 1 < args.size {
        groupFilter = args[i + 1];
      } else if args[i] == "--threshold" && i + 1 < args.size {
        try { threshold = args[i + 1]:int; } catch { }
      }
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (found, entryPath, value) = KeePassXC.resolve(dbPath, query, password, groupFilter, threshold);

    if found {
      // Output just the value (useful for piping, like keys get)
      writeln(value);
    } else {
      if entryPath != "" {
        printError("Best match '" + entryPath + "' scored below threshold (" + threshold:string + ")");
      } else {
        printError("No entries found matching '" + query + "'");
      }
    }
  }

  // Handle 'keys get <path>' - Retrieve a secret by entry path
  proc handleKeysGet(args: list(string)) {
    if args.size < 1 {
      printError("Missing entry path");
      writeln("Usage: remote-juggler keys get <entry-path>");
      writeln("Example: remote-juggler keys get RemoteJuggler/API/PERPLEXITY_API_KEY");
      return;
    }

    const entryPath = args[0];
    printDebug("Getting entry: " + entryPath);

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (found, value) = KeePassXC.getEntry(dbPath, entryPath, password);

    if found {
      // Output just the value (useful for piping)
      writeln(value);
    } else {
      printError("Entry not found: " + entryPath);
      writeln();
      writeln("Use 'remote-juggler keys search <query>' to find entries.");
    }
  }

  // Handle 'keys store <path>' - Store a secret at entry path
  proc handleKeysStore(args: list(string)) {
    if args.size < 1 {
      printError("Missing entry path");
      writeln("Usage: remote-juggler keys store <entry-path> [--value <value>]");
      writeln("If --value is not provided, reads from stdin.");
      return;
    }

    const entryPath = args[0];
    printDebug("Storing entry: " + entryPath);

    // Check for --value flag
    var value = "";
    var hasValueFlag = false;
    for i in 1..<args.size {
      if args[i] == "--value" && i + 1 < args.size {
        value = args[i + 1];
        hasValueFlag = true;
        break;
      }
    }

    if !hasValueFlag {
      // Read value from stdin
      writeln("Enter value for ", bold(entryPath), " (input hidden):");
      write("> ");
      if !stdin.readLine(value) {
        printError("Failed to read value");
        return;
      }
      value = value.strip();
    }

    if value == "" {
      printError("Value cannot be empty");
      return;
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    if KeePassXC.setEntry(dbPath, entryPath, password, value) {
      printSuccess("Stored entry: " + entryPath);
    } else {
      printError("Failed to store entry: " + entryPath);
    }
  }

  // Handle 'keys list [group]' - List entries in a group
  proc handleKeysList(args: list(string)) {
    const group = if args.size > 0 then args[0] else "RemoteJuggler";
    printDebug("Listing entries in: " + group);

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (listOk, entries) = KeePassXC.listEntries(dbPath, group, password);

    if !listOk {
      printError("Failed to list entries in: " + group);
      return;
    }

    writeln(bold("Entries in "), bold(group), bold(":"));
    writeln();

    if entries.size == 0 {
      writeln(dim("  (empty)"));
    } else {
      for entry in entries {
        if entry.endsWith("/") {
          // Group
          writeln("  ", blue(entry));
        } else {
          // Entry
          writeln("  ", entry);
        }
      }
    }

    writeln();
    writeln(dim(entries.size:string + " item(s)"));
  }

  // Handle 'keys ingest <path>' - Ingest a .env file
  proc handleKeysIngest(args: list(string)) {
    if args.size < 1 {
      printError("Missing file path");
      writeln("Usage: remote-juggler keys ingest <path-to-.env-file>");
      return;
    }

    const envFilePath = expandPath(args[0]);
    printDebug("Ingesting .env file: " + envFilePath);

    // Check file exists
    try {
      if !FileSystem.exists(envFilePath) {
        printError("File not found: " + envFilePath);
        return;
      }
    } catch {
      printError("Cannot access file: " + envFilePath);
      return;
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    writeln("Ingesting: ", bold(envFilePath));
    writeln();

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated) = KeePassXC.ingestEnvFile(dbPath, envFilePath, password);

    if added > 0 || updated > 0 {
      printSuccess("Ingested .env file");
      if added > 0 {
        writeln("  Added:   ", green(added:string), " entries");
      }
      if updated > 0 {
        writeln("  Updated: ", cyan(updated:string), " entries");
      }
    } else {
      writeln(dim("No new or changed entries found in "), envFilePath);
    }
  }

  // Handle 'keys delete <path>' - Delete an entry from the key store
  proc handleKeysDelete(args: list(string)) {
    if args.size < 1 {
      printError("Missing entry path");
      writeln("Usage: remote-juggler keys delete <entry-path>");
      return;
    }

    const entryPath = args[0];
    printDebug("Deleting entry: " + entryPath);

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();

    // Confirm entry exists before deleting
    const (existsOk, _) = KeePassXC.getEntry(dbPath, entryPath, password);
    if !existsOk {
      printError("Entry not found: " + entryPath);
      writeln("Use 'remote-juggler keys search <query>' to find entries.");
      return;
    }

    if KeePassXC.deleteEntry(dbPath, entryPath, password) {
      printSuccess("Deleted entry: " + entryPath);
    } else {
      printError("Failed to delete entry: " + entryPath);
    }
  }

  // Handle 'keys crawl [dirs]' - Crawl directories for .env files
  proc handleKeysCrawl(args: list(string)) {
    printDebug("Crawling for .env files");

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    // Parse optional --dirs flag
    var dirs: list(string);
    for i in 0..<args.size {
      if args[i] == "--dirs" {
        // Collect remaining args as directories
        for j in i+1..<args.size {
          dirs.pushBack(expandPath(args[j]));
        }
        break;
      } else {
        // Positional args are directories
        dirs.pushBack(expandPath(args[i]));
      }
    }

    writeln("Crawling for .env files...");
    writeln();

    const dbPath = KeePassXC.getDatabasePath();
    const (filesFound, totalAdded, totalUpdated) = KeePassXC.crawlEnvFiles(dbPath, password, dirs);

    if filesFound > 0 {
      printSuccess("Crawl complete");
      writeln("  Files found: ", green(filesFound:string));
      writeln("  Added:       ", green(totalAdded:string), " entries");
      writeln("  Updated:     ", cyan(totalUpdated:string), " entries");
    } else {
      writeln(dim("No .env files found"));
    }
  }

  // Handle 'keys discover [--types env|ssh|all]' - Auto-discover credentials
  proc handleKeysDiscover(args: list(string)) {
    printDebug("Discovering credentials");

    // Parse --types flag
    var discoverTypes = "all";
    for i in 0..<args.size {
      if args[i] == "--types" && i + 1 < args.size {
        discoverTypes = args[i + 1];
        break;
      }
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    writeln("Discovering credentials...");
    writeln();

    var totalDiscovered = 0;

    if discoverTypes == "env" || discoverTypes == "all" {
      // Ensure Discovered group exists
      KeePassXC.ensureGroup(dbPath, "RemoteJuggler/Discovered", password);

      const envDiscovered = KeePassXC.discoverEnvCredentials(dbPath, password);
      writeln("  Environment variables: ", green(envDiscovered:string), " credential(s)");
      totalDiscovered += envDiscovered;
    }

    if discoverTypes == "ssh" || discoverTypes == "all" {
      const sshDiscovered = KeePassXC.discoverSSHKeys(dbPath, password);
      writeln("  SSH keys:              ", green(sshDiscovered:string), " key(s)");
      totalDiscovered += sshDiscovered;
    }

    if discoverTypes == "sops" || discoverTypes == "all" {
      if KeePassXC.isSopsReady() {
        writeln("  SOPS:                  ", green("available"), " (discovered via crawl)");
      } else {
        writeln("  SOPS:                  ", dim("not available"), " (missing sops/age)");
      }
    }

    writeln();
    if totalDiscovered > 0 {
      printSuccess("Discovered " + totalDiscovered:string + " credential(s)");
    } else {
      writeln(dim("No new credentials discovered"));
    }
  }

  // Handle 'keys export <group> [--format env|json]' - Export group entries
  proc handleKeysExport(args: list(string)) {
    if args.size < 1 {
      printError("Missing group path");
      writeln("Usage: remote-juggler keys export <group> [--format env|json]");
      writeln("Example: remote-juggler keys export RemoteJuggler/API --format env");
      return;
    }

    const group = args[0];
    printDebug("Exporting group: " + group);

    // Parse --format flag
    var format = "env";
    for i in 1..<args.size {
      if args[i] == "--format" && i + 1 < args.size {
        format = args[i + 1];
        break;
      }
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (exportOk, content) = KeePassXC.exportEntries(dbPath, group, password, format);

    if exportOk {
      writeln(content);
    } else {
      printError("Failed to export entries from: " + group);
    }
  }

  // Handle 'keys sops-ingest <file>' - Ingest a SOPS-encrypted file
  proc handleKeysSopsIngest(args: list(string)) {
    if args.size < 1 {
      printError("Missing file path");
      writeln("Usage: remote-juggler keys sops-ingest <path-to-sops-file>");
      return;
    }

    if !KeePassXC.isSopsReady() {
      printError("SOPS integration not available");
      writeln("Install both 'sops' and 'age' binaries to use SOPS integration.");
      return;
    }

    const sopsFilePath = expandPath(args[0]);
    printDebug("Ingesting SOPS file: " + sopsFilePath);

    // Check file exists
    try {
      if !FileSystem.exists(sopsFilePath) {
        printError("File not found: " + sopsFilePath);
        return;
      }
    } catch {
      printError("Cannot access file: " + sopsFilePath);
      return;
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    writeln("Ingesting SOPS file: ", bold(sopsFilePath));
    writeln();

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated) = KeePassXC.ingestSopsFile(dbPath, sopsFilePath, password);

    if added > 0 || updated > 0 {
      printSuccess("Ingested SOPS file");
      if added > 0 {
        writeln("  Added:   ", green(added:string), " entries");
      }
      if updated > 0 {
        writeln("  Updated: ", cyan(updated:string), " entries");
      }
    } else {
      writeln(dim("No new or changed entries found in "), sopsFilePath);
    }
  }

  // Handle 'keys sops-sync <file>' - Sync a SOPS file bidirectionally
  proc handleKeysSopsSync(args: list(string)) {
    if args.size < 1 {
      printError("Missing file path");
      writeln("Usage: remote-juggler keys sops-sync <path-to-sops-file>");
      return;
    }

    if !KeePassXC.isSopsReady() {
      printError("SOPS integration not available");
      writeln("Install both 'sops' and 'age' binaries to use SOPS integration.");
      return;
    }

    const sopsFilePath = expandPath(args[0]);
    printDebug("Syncing SOPS file: " + sopsFilePath);

    // Check file exists
    try {
      if !FileSystem.exists(sopsFilePath) {
        printError("File not found: " + sopsFilePath);
        return;
      }
    } catch {
      printError("Cannot access file: " + sopsFilePath);
      return;
    }

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    writeln("Syncing SOPS file: ", bold(sopsFilePath));
    writeln();

    const dbPath = KeePassXC.getDatabasePath();
    const (added, updated, deleted) = KeePassXC.syncSopsFile(dbPath, sopsFilePath, password);

    if added > 0 || updated > 0 || deleted > 0 {
      printSuccess("Synced SOPS file");
      if added > 0 {
        writeln("  Added:   ", green(added:string), " entries");
      }
      if updated > 0 {
        writeln("  Updated: ", cyan(updated:string), " entries");
      }
      if deleted > 0 {
        writeln("  Deleted: ", yellow(deleted:string), " entries");
      }
    } else {
      writeln(dim("No changes detected in "), sopsFilePath);
    }
  }

  // Handle 'keys sops-export' - Export age key for SOPS recipients
  proc handleKeysSopsExport(args: list(string)) {
    if !KeePassXC.isSopsReady() {
      printError("SOPS integration not available");
      writeln("Install both 'sops' and 'age' binaries to use SOPS integration.");
      return;
    }

    // Parse optional --group flag
    var group = "RemoteJuggler/SOPS";
    for i in 0..<args.size {
      if args[i] == "--group" && i + 1 < args.size {
        group = args[i + 1];
        break;
      }
    }

    printDebug("Exporting SOPS age key from group: " + group);

    // Auto-unlock
    if !KeePassXC.canAutoUnlock() {
      printError("Cannot auto-unlock key store");
      writeln("Ensure HSM and YubiKey are available.");
      return;
    }

    const (ok, password) = KeePassXC.autoUnlock();
    if !ok {
      printError("Failed to unlock key store");
      return;
    }

    const dbPath = KeePassXC.getDatabasePath();
    const (exportOk, pubKey) = KeePassXC.exportSopsAgeKey(dbPath, password, group);

    if exportOk {
      writeln(bold("Age Public Key (for .sops.yaml recipients):"));
      writeln();
      writeln(pubKey);
    } else {
      printError(pubKey); // Error message is in pubKey on failure
    }
  }

  // Handle 'unseal-pin' command (used by pinentry-remotejuggler.py)
  proc handleUnsealPin(args: list(string)) {
    if args.size < 1 {
      // Silent failure for pinentry compatibility
      halt(1);
    }

    const name = args[0];
    printDebug("Unsealing PIN for: " + name);

    // This command outputs PIN to stdout for pinentry to capture
    // No other output should go to stdout
    const result = TrustedWorkstation.unsealPinForPinentry(name);

    if !result.success {
      // Exit with error code, no output
      halt(1);
    }
    // Success - PIN was written to stdout by unsealPinForPinentry
  }
}

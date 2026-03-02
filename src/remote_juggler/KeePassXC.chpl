/*
 * KeePassXC.chpl - KeePassXC Credential Authority Module
 *
 * Wraps keepassxc-cli to provide CRUD operations on a kdbx database.
 * RemoteJuggler uses this as a trusted credential authority for ALL secrets:
 * SSH keys, GPG passphrases, API tokens, .env files, and infrastructure creds.
 *
 * Trust Model:
 *   TPM/SecureEnclave stores the kdbx master password.
 *   + YubiKey presence (ykman info succeeds)
 *   = Machine is TRUSTED -> agents get credential access without prompts.
 *
 * Database Layout (default path: ~/.remotejuggler/keys.kdbx):
 *   RemoteJuggler/
 *   ├── SSH/           - SSH key metadata, passphrases
 *   ├── GPG/           - GPG key IDs, passphrases
 *   ├── Tokens/        - Provider PATs (GitLab, GitHub)
 *   ├── API/           - API keys (PERPLEXITY_API_KEY, etc.)
 *   ├── Infrastructure/ - sudo passwords, kubeconfig creds
 *   └── Environments/  - Ingested .env files (path-based layout)
 *
 * All keepassxc-cli commands receive the master password via stdin pipe,
 * never as a CLI argument.
 *
 * :author: RemoteJuggler Team
 * :version: 2.0.0
 */
prototype module KeePassXC {
  use IO;
  use List;
  use FileSystem;
  use Subprocess;
  use Path;
  use super.Core only getEnvVar, getEnvOrDefault, expandTilde, verboseLog;
  public use super.HSM;
  import super.Manifest;

  // ============================================================================
  // Constants
  // ============================================================================

  /* Default database path */
  param DEFAULT_DB_PATH = "~/.remotejuggler/keys.kdbx";

  /* HSM identity label for the kdbx master password */
  param KDBX_HSM_IDENTITY = "kdbx-master";

  /* Default group hierarchy to create on bootstrap */
  const BOOTSTRAP_GROUPS = [
    "RemoteJuggler",
    "RemoteJuggler/SSH",
    "RemoteJuggler/GPG",
    "RemoteJuggler/Tokens",
    "RemoteJuggler/Tokens/GitLab",
    "RemoteJuggler/Tokens/GitHub",
    "RemoteJuggler/API",
    "RemoteJuggler/Infrastructure",
    "RemoteJuggler/Infrastructure/sudo",
    "RemoteJuggler/Infrastructure/kubeconfig",
    "RemoteJuggler/Infrastructure/ansible-vault",
    "RemoteJuggler/Environments",
    "RemoteJuggler/SOPS"
  ];

  // ============================================================================
  // Search Result Type
  // ============================================================================

  /*
   * SearchResult - A single fuzzy search match.
   *
   * :var entryPath: Full path within the database (e.g., "RemoteJuggler/API/PERPLEXITY_API_KEY")
   * :var title: Entry title
   * :var matchContext: Brief description of why this matched
   * :var matchField: Which field matched (path, title, username, notes, url)
   * :var score: Relevance score (higher = better match)
   */
  record SearchResult {
    var entryPath: string = "";
    var title: string = "";
    var matchContext: string = "";
    var matchField: string = "path";
    var score: int = 0;

    proc init() {
      this.entryPath = "";
      this.title = "";
      this.matchContext = "";
      this.matchField = "path";
      this.score = 0;
    }

    proc init(entryPath: string, title: string, matchContext: string, score: int) {
      this.entryPath = entryPath;
      this.title = title;
      this.matchContext = matchContext;
      this.matchField = "path";
      this.score = score;
    }

    proc init(entryPath: string, title: string, matchContext: string, matchField: string, score: int) {
      this.entryPath = entryPath;
      this.title = title;
      this.matchContext = matchContext;
      this.matchField = matchField;
      this.score = score;
    }
  }

  // ============================================================================
  // Fuzzy Matching
  // ============================================================================

  /*
   * Compute Levenshtein edit distance between two strings.
   *
   * :arg s1: First string
   * :arg s2: Second string
   * :returns: Minimum number of single-character edits
   */
  proc levenshteinDistance(s1: string, s2: string): int {
    const len1 = s1.size;
    const len2 = s2.size;

    if len1 == 0 then return len2;
    if len2 == 0 then return len1;

    // Use two rows of the DP matrix
    var prevRow: [0..len2] int;
    var currRow: [0..len2] int;

    for j in 0..len2 {
      prevRow[j] = j;
    }

    for i in 1..len1 {
      currRow[0] = i;
      for j in 1..len2 {
        const cost = if s1[i-1] == s2[j-1] then 0 else 1;
        currRow[j] = min(min(currRow[j-1] + 1, prevRow[j] + 1), prevRow[j-1] + cost);
      }
      for j in 0..len2 {
        prevRow[j] = currRow[j];
      }
    }

    return prevRow[len2];
  }

  /*
   * Compute a fuzzy match score between a query and a candidate string.
   *
   * Scoring tiers:
   * - Exact match (case-insensitive): 100
   * - Substring match: 70
   * - Word boundary match (initials/acronym): 60
   * - Levenshtein distance <= 2: 40
   * - Levenshtein distance <= 4: 20
   * - No match: 0
   *
   * :arg query: Search query (lowercased by caller)
   * :arg candidate: Candidate string to match against
   * :returns: Score (0-100, higher = better)
   */
  proc fuzzyScore(query: string, candidate: string): int {
    const lowerCandidate = candidate.toLower();
    const lowerQuery = query.toLower();

    // Exact match
    if lowerCandidate == lowerQuery then return 100;

    // Substring match
    if lowerCandidate.find(lowerQuery) != -1 then return 70;

    // Word boundary match: check if query chars match word-start chars
    // e.g., "gl_tok" matches "GITLAB_TOKEN" via G_T boundaries
    if wordBoundaryMatch(lowerQuery, lowerCandidate) then return 60;

    // Levenshtein on just the title (last path component) for shorter strings
    const dist = levenshteinDistance(lowerQuery, lowerCandidate);
    if dist <= 2 then return 40;
    if dist <= 4 then return 20;

    return 0;
  }

  /*
   * Check if query matches candidate at word boundaries.
   *
   * Word boundaries are: start of string, after '_', '-', '/', '.', uppercase transitions.
   * Each query char must match a boundary char in order.
   *
   * :arg query: Lowercased query
   * :arg candidate: Lowercased candidate
   * :returns: true if boundary match found
   */
  proc wordBoundaryMatch(query: string, candidate: string): bool {
    if query.size == 0 then return false;

    // Extract boundary characters from candidate
    var boundaries: list(string);
    var prevWasBoundary = true; // start of string is a boundary
    for i in 0..<candidate.size {
      const ch = candidate[i];
      if prevWasBoundary && ch != '_' && ch != '-' && ch != '/' && ch != '.' {
        boundaries.pushBack(ch);
      }
      prevWasBoundary = (ch == '_' || ch == '-' || ch == '/' || ch == '.');
    }

    // Check if query chars appear in boundaries in order
    var qi = 0;
    for bi in 0..<boundaries.size {
      if qi < query.size && boundaries[bi] == query[qi] {
        qi += 1;
      }
    }
    return qi == query.size;
  }

  // ============================================================================
  // Detection & Availability
  // ============================================================================

  /*
   * Check if keepassxc-cli is available on the system.
   *
   * :returns: true if keepassxc-cli is found in PATH
   */
  proc isAvailable(): bool {
    try {
      var p = spawn(["which", "keepassxc-cli"],
                    stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Get the configured database path.
   *
   * Reads from config settings, falls back to default.
   *
   * :returns: Expanded database file path
   */
  proc getDatabasePath(): string {
    // Try reading from config
    const configDbPath = getEnvVar("REMOTE_JUGGLER_KDBX_PATH");
    if configDbPath != "" {
      return expandTilde(configDbPath);
    }
    return expandTilde(DEFAULT_DB_PATH);
  }

  /*
   * Check if the database file exists.
   *
   * :returns: true if the kdbx file exists at the configured path
   */
  proc databaseExists(): bool {
    const dbPath = getDatabasePath();
    try {
      return FileSystem.exists(dbPath);
    } catch {
      return false;
    }
  }

  // ============================================================================
  // Session Cache
  // ============================================================================

  /* Cached session password to avoid repeated HSM round-trips */
  private var _sessionPassword: string = "";
  /* Timestamp when session was cached (seconds since epoch) */
  private var _sessionTimestamp: int = 0;
  /* Session TTL in seconds */
  param SESSION_TTL_SECONDS = 30;

  /*
   * Get the session password, returning cached version if within TTL.
   *
   * :returns: (success, password)
   */
  proc getSessionPassword(): (bool, string) {
    if _sessionPassword != "" {
      // Check if still within TTL
      use Time;
      const now = dateTime.now().second + dateTime.now().minute * 60 + dateTime.now().hour * 3600;
      if (now - _sessionTimestamp) < SESSION_TTL_SECONDS {
        return (true, _sessionPassword);
      }
      // Expired - clear it
      _sessionPassword = "";
      _sessionTimestamp = 0;
    }
    return (false, "");
  }

  /*
   * Cache the session password with current timestamp.
   */
  proc cacheSessionPassword(password: string) {
    use Time;
    _sessionPassword = password;
    _sessionTimestamp = dateTime.now().second + dateTime.now().minute * 60 + dateTime.now().hour * 3600;
  }

  /*
   * Explicitly clear the session cache.
   */
  proc clearSession() {
    _sessionPassword = "";
    _sessionTimestamp = 0;
  }

  // ============================================================================
  // Unlock Flow (TPM/SE + YubiKey)
  // ============================================================================

  /*
   * Check if auto-unlock is possible.
   *
   * Auto-unlock requires:
   * 1. HSM available (TPM or Secure Enclave)
   * 2. Master password sealed in HSM
   * 3. YubiKey present (physical security anchor)
   *
   * :returns: true if all conditions met for auto-unlock
   */
  proc canAutoUnlock(): bool {
    // Check HSM
    if hsm_is_available() == 0 {
      return false;
    }

    // Check if master password is sealed
    if hsm_has_pin(KDBX_HSM_IDENTITY) == 0 {
      return false;
    }

    // Check YubiKey presence
    return isYubiKeyPresent();
  }

  /*
   * Check if a YubiKey is currently connected.
   *
   * Supports REMOTE_JUGGLER_YKMAN_PATH env var to override the ykman binary,
   * enabling CI testing with a mock script.
   *
   * :returns: true if ykman can detect a YubiKey
   */
  proc isYubiKeyPresent(): bool {
    const ykmanPath = getEnvOrDefault("REMOTE_JUGGLER_YKMAN_PATH", "ykman");
    try {
      var p = spawn([ykmanPath, "info"],
                    stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Check if the sops binary is available on the system.
   *
   * Supports REMOTE_JUGGLER_SOPS_PATH env var to override the binary,
   * enabling CI testing with a mock script.
   *
   * :returns: true if sops is found
   */
  proc isSopsAvailable(): bool {
    const sopsPath = getEnvOrDefault("REMOTE_JUGGLER_SOPS_PATH", "sops");
    try {
      var p = spawn([sopsPath, "--version"],
                    stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Check if the age binary is available on the system.
   *
   * Supports REMOTE_JUGGLER_AGE_PATH env var to override the binary,
   * enabling CI testing with a mock script.
   *
   * :returns: true if age is found
   */
  proc isAgeAvailable(): bool {
    const agePath = getEnvOrDefault("REMOTE_JUGGLER_AGE_PATH", "age");
    try {
      var p = spawn([agePath, "--version"],
                    stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  /*
   * Check if both sops and age are available.
   * This is the prerequisite gate for all SOPS operations.
   *
   * :returns: true if both sops and age binaries found
   */
  proc isSopsReady(): bool {
    return isSopsAvailable() && isAgeAvailable();
  }

  /*
   * Auto-unlock the database by retrieving the master password from HSM.
   *
   * Uses session caching to avoid repeated HSM round-trips within the TTL.
   *
   * :returns: (success, masterPassword) - password is empty on failure
   */
  proc autoUnlock(): (bool, string) {
    // Check session cache first
    const (cached, cachedPassword) = getSessionPassword();
    if cached {
      return (true, cachedPassword);
    }

    if !canAutoUnlock() {
      return (false, "");
    }

    // Retrieve sealed master password from HSM
    const (result, password) = hsm_retrieve_pin(KDBX_HSM_IDENTITY);
    if result != HSM_SUCCESS {
      verboseLog("KeePassXC: Failed to retrieve master password from HSM: ",
                 hsm_error_message(result));
      return (false, "");
    }

    // Cache for subsequent calls within TTL
    cacheSessionPassword(password);

    return (true, password);
  }

  /*
   * Check if the database is currently accessible (can be opened).
   *
   * :returns: true if auto-unlock succeeds and database can be read
   */
  proc isUnlocked(): bool {
    if !databaseExists() {
      return false;
    }

    const (ok, password) = autoUnlock();
    if !ok {
      return false;
    }

    const dbPath = getDatabasePath();
    // Test by listing root group
    const (listOk, _) = listEntries(dbPath, "", password);
    return listOk;
  }

  // ============================================================================
  // CRUD Operations
  // ============================================================================

  /*
   * Get a secret value from the database.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg entryPath: Entry path within the database
   * :arg password: Master password for the database
   * :returns: (success, secretValue)
   */
  proc getEntry(dbPath: string, entryPath: string, password: string): (bool, string) {
    try {
      var p = spawn(["keepassxc-cli", "show", "-s", "-a", "Password", dbPath, entryPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var value: string;
        p.stdout.readAll(value);
        return (true, value.strip());
      } else {
        var errMsg: string;
        p.stderr.readAll(errMsg);
        verboseLog("KeePassXC getEntry failed: ", errMsg.strip());
        return (false, "");
      }
    } catch e {
      verboseLog("KeePassXC getEntry error: ", e.message());
      return (false, "");
    }
  }

  /*
   * Store or update a secret in the database.
   *
   * Creates the entry if it doesn't exist, updates if it does.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg entryPath: Entry path within the database
   * :arg password: Master password
   * :arg value: Secret value to store
   * :returns: true on success
   */
  proc setEntry(dbPath: string, entryPath: string, password: string, value: string): bool {
    // Try to edit first (entry exists)
    try {
      var p = spawn(["keepassxc-cli", "edit", "-p", dbPath, entryPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n" + value + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        return true;
      }
    } catch { }

    // Entry doesn't exist, create it
    try {
      var p = spawn(["keepassxc-cli", "add", "-p", dbPath, entryPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n" + value + "\n");
      p.stdin.close();
      p.wait();

      return p.exitCode == 0;
    } catch e {
      verboseLog("KeePassXC setEntry error: ", e.message());
      return false;
    }
  }

  /*
   * Delete an entry from the database.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg entryPath: Entry path within the database
   * :arg password: Master password
   * :returns: true on success
   */
  proc deleteEntry(dbPath: string, entryPath: string, password: string): bool {
    try {
      var p = spawn(["keepassxc-cli", "rm", dbPath, entryPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();

      return p.exitCode == 0;
    } catch e {
      verboseLog("KeePassXC deleteEntry error: ", e.message());
      return false;
    }
  }

  /*
   * List entries in a group.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg group: Group path (empty for root)
   * :arg password: Master password
   * :returns: (success, list of entry names)
   */
  proc listEntries(dbPath: string, group: string, password: string): (bool, list(string)) {
    var entries: list(string);
    try {
      var args: list(string);
      args.pushBack("keepassxc-cli");
      args.pushBack("ls");
      args.pushBack(dbPath);
      if group != "" {
        args.pushBack(group);
      }

      var p = spawn(args.toArray(),
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed != "" {
            entries.pushBack(trimmed);
          }
        }
        return (true, entries);
      } else {
        return (false, entries);
      }
    } catch e {
      verboseLog("KeePassXC listEntries error: ", e.message());
      return (false, entries);
    }
  }

  // ============================================================================
  // Entry Metadata
  // ============================================================================

  /*
   * Get entry metadata (username, notes, URL) without exposing the password.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg entryPath: Entry path within the database
   * :arg password: Master password
   * :returns: (success, username, notes, url)
   */
  proc getEntryMetadata(dbPath: string, entryPath: string, password: string): (bool, string, string, string) {
    try {
      var p = spawn(["keepassxc-cli", "show", "-a", "UserName", "-a", "Notes", "-a", "URL", dbPath, entryPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);
        var lines = output.split("\n");
        var username = if lines.size > 0 then lines[0].strip() else "";
        var notes = if lines.size > 1 then lines[1].strip() else "";
        var url = if lines.size > 2 then lines[2].strip() else "";
        return (true, username, notes, url);
      }
    } catch { }
    return (false, "", "", "");
  }

  // ============================================================================
  // Search
  // ============================================================================

  /*
   * Search the database using keepassxc-cli search + in-process fuzzy matching.
   *
   * Strategy (fast path first):
   * 1. keepassxc-cli search for exact/prefix matches
   * 2. Fuzzy match all entries using Levenshtein + word boundary scoring
   * 3. If few results, search entry metadata fields (username, notes, URL)
   *
   * :arg dbPath: Path to the kdbx file
   * :arg query: Search query string
   * :arg password: Master password
   * :arg group: Optional group to restrict search (empty = all)
   * :returns: List of ranked SearchResult (no secrets included)
   */
  proc search(dbPath: string, query: string, password: string, group: string = ""): list(SearchResult) {
    var results: list(SearchResult);
    const lowerQuery = query.toLower();

    // Step 1: keepassxc-cli search for exact/prefix matches
    try {
      var p = spawn(["keepassxc-cli", "search", dbPath, query],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed != "" {
            // Filter by group if specified
            if group != "" && !trimmed.startsWith(group) then continue;

            var title = trimmed;
            const lastSlash = trimmed.rfind("/");
            if lastSlash >= 0 {
              title = trimmed[lastSlash+1..];
            }
            results.pushBack(new SearchResult(
              entryPath = trimmed,
              title = title,
              matchContext = "exact match",
              score = 100
            ));
          }
        }
      }
    } catch { }

    // Step 2: Fuzzy matching against all entries
    if results.size < 10 {
      // Build list of all entries via recursive listing
      var lsArgs: list(string);
      lsArgs.pushBack("keepassxc-cli");
      lsArgs.pushBack("ls");
      lsArgs.pushBack("-R");
      lsArgs.pushBack(dbPath);
      if group != "" {
        lsArgs.pushBack(group);
      }

      try {
        var p = spawn(lsArgs.toArray(),
                      stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.close);
        p.stdin.write(password + "\n");
        p.stdin.close();
        p.wait();

        if p.exitCode == 0 {
          var output: string;
          p.stdout.readAll(output);

          // Track current group path for ls -R output
          var currentGroup = if group != "" then group else "";
          var unmatchedEntries: list(string);

          for line in output.split("\n") {
            const trimmed = line.strip();
            if trimmed == "" then continue;

            // Group headers end with /
            if trimmed.endsWith("/") {
              currentGroup = trimmed[0..#(trimmed.size-1)];
              continue;
            }

            // Build full entry path
            var fullPath = if currentGroup != "" then currentGroup + "/" + trimmed else trimmed;

            // Skip entries already found in Step 1
            var alreadyFound = false;
            for existing in results {
              if existing.entryPath == fullPath {
                alreadyFound = true;
                break;
              }
            }
            if alreadyFound then continue;

            // Compute fuzzy score against both title and full path
            var title = trimmed;
            const titleScore = fuzzyScore(lowerQuery, title);
            const pathScore = fuzzyScore(lowerQuery, fullPath);
            const bestScore = max(titleScore, pathScore);

            if bestScore > 0 {
              const context = if bestScore >= 70 then "substring match"
                             else if bestScore >= 60 then "word boundary match"
                             else if bestScore >= 40 then "fuzzy match (close)"
                             else "fuzzy match";
              results.pushBack(new SearchResult(
                entryPath = fullPath,
                title = title,
                matchContext = context,
                matchField = "path",
                score = bestScore
              ));
            } else {
              // Track for potential metadata search in Step 3
              unmatchedEntries.pushBack(fullPath);
            }
          }

          // Step 3: If few results, search entry metadata fields
          if results.size < 3 {
            for entryPath in unmatchedEntries {
              const (metaOk, username, notes, url) = getEntryMetadata(dbPath, entryPath, password);
              if !metaOk then continue;

              var bestMetaScore = 0;
              var bestMetaField = "";
              var bestMetaContext = "";

              if username != "" {
                const uscore = fuzzyScore(lowerQuery, username);
                if uscore > bestMetaScore {
                  bestMetaScore = uscore;
                  bestMetaField = "username";
                  bestMetaContext = "username: " + username;
                }
              }

              if notes != "" {
                const nscore = fuzzyScore(lowerQuery, notes);
                if nscore > bestMetaScore {
                  bestMetaScore = nscore;
                  bestMetaField = "notes";
                  bestMetaContext = "notes match";
                }
              }

              if url != "" {
                const uscore = fuzzyScore(lowerQuery, url);
                if uscore > bestMetaScore {
                  bestMetaScore = uscore;
                  bestMetaField = "url";
                  bestMetaContext = "url: " + url;
                }
              }

              if bestMetaScore > 0 {
                var title = entryPath;
                const lastSlash = entryPath.rfind("/");
                if lastSlash >= 0 {
                  title = entryPath[lastSlash+1..];
                }
                results.pushBack(new SearchResult(
                  entryPath = entryPath,
                  title = title,
                  matchContext = bestMetaContext,
                  matchField = bestMetaField,
                  score = bestMetaScore
                ));
              }

              // Stop metadata search after checking 20 entries (performance)
              if results.size >= 10 then break;
            }
          }
        }
      } catch { }
    }

    // Sort by score descending (simple bubble sort for small lists)
    for i in 0..<results.size {
      for j in i+1..<results.size {
        if results[j].score > results[i].score {
          const tmp = results[i];
          results[i] = results[j];
          results[j] = tmp;
        }
      }
    }

    return results;
  }

  // ============================================================================
  // Resolve (Search + Get Combined)
  // ============================================================================

  /*
   * Resolve a query to a secret value in one operation.
   *
   * Searches for the best match above the threshold, then retrieves its value.
   * Eliminates the two-call pattern (search then get) for agents.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg query: Search query string
   * :arg password: Master password
   * :arg group: Optional group to restrict search
   * :arg threshold: Minimum score to accept (default 40)
   * :returns: (success, entryPath, value) - entryPath shows what matched
   */
  proc resolve(dbPath: string, query: string, password: string,
               group: string = "", threshold: int = 40): (bool, string, string) {
    const results = search(dbPath, query, password, group);

    if results.size == 0 {
      return (false, "", "");
    }

    // Pick best match above threshold
    const best = results[0];
    if best.score < threshold {
      return (false, "", "");
    }

    // Retrieve the value
    const (found, value) = getEntry(dbPath, best.entryPath, password);
    if found {
      return (true, best.entryPath, value);
    }
    return (false, best.entryPath, "");
  }

  // ============================================================================
  // Bootstrap
  // ============================================================================

  /*
   * Bootstrap a new kdbx database.
   *
   * 1. Generate random master password (32 chars)
   * 2. Create database with keepassxc-cli db-create
   * 3. Create group hierarchy
   * 4. Seal master password in TPM/SE
   * 5. Verify round-trip
   *
   * :arg dbPath: Path for the new database (created if not exists)
   * :arg sealInHSM: Whether to seal master password in HSM
   * :returns: (success, message)
   */
  proc bootstrapDatabase(dbPath: string, sealInHSM: bool = true): (bool, string) {
    if !isAvailable() {
      return (false, "keepassxc-cli not found in PATH. Install KeePassXC first.");
    }

    // Check if database already exists
    try {
      if FileSystem.exists(dbPath) {
        return (false, "Database already exists at " + dbPath +
                ". Use a different path or delete the existing database.");
      }
    } catch { }

    // Ensure parent directory exists
    const parentDir = dbPath[0..#dbPath.rfind("/"):int];
    try {
      if !FileSystem.exists(parentDir) {
        FileSystem.mkdir(parentDir, parents=true);
      }
    } catch e {
      return (false, "Failed to create directory: " + e.message());
    }

    // Generate random master password (32 chars, alphanumeric + symbols)
    var masterPassword = generateRandomPassword(32);

    // Create database
    try {
      var p = spawn(["keepassxc-cli", "db-create", "--set-password", dbPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.stdin.write(masterPassword + "\n" + masterPassword + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode != 0 {
        var errMsg: string;
        p.stderr.readAll(errMsg);
        return (false, "Failed to create database: " + errMsg.strip());
      }
    } catch e {
      return (false, "Error creating database: " + e.message());
    }

    // Create group hierarchy
    for group in BOOTSTRAP_GROUPS {
      try {
        var p = spawn(["keepassxc-cli", "mkdir", dbPath, group],
                      stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.close);
        p.stdin.write(masterPassword + "\n");
        p.stdin.close();
        p.wait();
      } catch {
        // Non-fatal: group may already exist
      }
    }

    // Seal master password in HSM if requested
    if sealInHSM {
      if hsm_is_available() != 0 {
        // Initialize PCR binding (PCR 7 = Secure Boot) before sealing
        const pcrResult = hsmSetPcrBinding(0x0080);
        if pcrResult != HSM_SUCCESS {
          verboseLog("KeePassXC: PCR binding not available (non-fatal): ",
                     hsm_error_message(pcrResult));
        }

        const sealResult = sealMasterPassword(KDBX_HSM_IDENTITY, masterPassword);
        if !sealResult {
          // Non-fatal: database is created, just can't auto-unlock
          return (true, "Database created at " + dbPath +
                  "\nWARNING: Failed to seal master password in HSM. " +
                  "Auto-unlock will not be available. " +
                  "Master password: " + masterPassword);
        }
      } else {
        return (true, "Database created at " + dbPath +
                "\nWARNING: No HSM available. Master password not sealed. " +
                "Store it securely: " + masterPassword);
      }
    }

    // Verify round-trip
    const (listOk, _) = listEntries(dbPath, "RemoteJuggler", masterPassword);
    if !listOk {
      return (false, "Database created but verification failed. " +
              "Master password: " + masterPassword);
    }

    // Clear master password from memory
    masterPassword = "";

    return (true, "Database created and sealed successfully at " + dbPath +
            "\nMaster password sealed in HSM (auto-unlock enabled)." +
            "\nGroup hierarchy created.");
  }

  /*
   * Seal the kdbx master password in TPM/SecureEnclave.
   *
   * :arg identity: HSM identity label
   * :arg masterPassword: The master password to seal
   * :returns: true on success
   */
  proc sealMasterPassword(identity: string, masterPassword: string): bool {
    const result = hsm_store_pin(identity, masterPassword, masterPassword.size);
    return result == HSM_SUCCESS;
  }

  // ============================================================================
  // .env File Ingestion
  // ============================================================================

  /*
   * Ingest a .env file into the database.
   *
   * Parses KEY=VALUE pairs and stores each as an entry under
   * RemoteJuggler/Environments/{canonical-path}/.env/{KEY}
   *
   * Handles: KEY=VALUE, KEY="quoted value", # comments, export prefixes
   *
   * :arg dbPath: Path to the kdbx file
   * :arg envFilePath: Path to the .env file to ingest
   * :arg password: Master password
   * :returns: (entriesAdded, entriesUpdated)
   */
  proc ingestEnvFile(dbPath: string, envFilePath: string, password: string): (int, int) {
    var added = 0;
    var updated = 0;

    // Read .env file
    var content: string;
    try {
      var f = open(envFilePath, ioMode.r);
      var reader = f.reader(locking=false);
      reader.readAll(content);
      reader.close();
      f.close();
    } catch e {
      verboseLog("KeePassXC ingestEnvFile: Failed to read ", envFilePath, ": ", e.message());
      return (0, 0);
    }

    // Canonical path for group name
    const canonicalPath = envFilePath.replace("/", "_").replace("~", "home");
    const groupPath = "RemoteJuggler/Environments/" + canonicalPath;

    // Ensure group exists
    try {
      var p = spawn(["keepassxc-cli", "mkdir", dbPath, groupPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();
    } catch { }

    // Parse and store each key-value pair
    for line in content.split("\n") {
      var trimmed = line.strip();

      // Skip comments and empty lines
      if trimmed == "" || trimmed.startsWith("#") {
        continue;
      }

      // Remove 'export ' prefix
      if trimmed.startsWith("export ") {
        trimmed = trimmed[7..];
      }

      // Find the = separator
      const eqPos = trimmed.find("=");
      if eqPos < 0 {
        continue;
      }

      const key = trimmed[0..#eqPos:int].strip();
      var value = trimmed[eqPos+1..].strip();

      // Remove surrounding quotes
      if value.size >= 2 {
        if (value.startsWith('"') && value.endsWith('"')) ||
           (value.startsWith("'") && value.endsWith("'")) {
          value = value[1..#(value.size-2)];
        }
      }

      if key == "" {
        continue;
      }

      // Store entry
      const entryPath = groupPath + "/" + key;

      // Check if entry already exists
      const (existsOk, existingValue) = getEntry(dbPath, entryPath, password);
      if existsOk {
        if existingValue != value {
          // Update
          if setEntry(dbPath, entryPath, password, value) {
            updated += 1;
          }
        }
        // Skip if unchanged
      } else {
        // Add new
        if setEntry(dbPath, entryPath, password, value) {
          added += 1;
        }
      }
    }

    return (added, updated);
  }

  /*
   * Sync a .env file with the database, detecting additions, updates, and deletions.
   *
   * Unlike ingestEnvFile which only adds/updates, this detects entries in the
   * database group that are no longer present in the .env file and deletes them.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg envFilePath: Path to the .env file
   * :arg password: Master password
   * :returns: (added, updated, deleted) counts
   */
  proc syncEnvFile(dbPath: string, envFilePath: string, password: string): (int, int, int) {
    // First, do the normal ingest (adds + updates)
    const (added, updated) = ingestEnvFile(dbPath, envFilePath, password);

    // Parse the .env file to get current keys
    var currentKeys: list(string);
    try {
      var f = open(envFilePath, ioMode.r);
      var reader = f.reader(locking=false);
      var content: string;
      reader.readAll(content);
      reader.close();
      f.close();

      for line in content.split("\n") {
        var trimmed = line.strip();
        if trimmed == "" || trimmed.startsWith("#") then continue;
        if trimmed.startsWith("export ") { trimmed = trimmed[7..]; }
        const eqPos = trimmed.find("=");
        if eqPos >= 0 {
          const key = trimmed[0..#eqPos:int].strip();
          if key != "" then currentKeys.pushBack(key);
        }
      }
    } catch {
      return (added, updated, 0);
    }

    // List entries currently in the database group
    const canonicalPath = envFilePath.replace("/", "_").replace("~", "home");
    const groupPath = "RemoteJuggler/Environments/" + canonicalPath;
    const (listOk, entries) = listEntries(dbPath, groupPath, password);

    var deleted = 0;
    if listOk {
      for entry in entries {
        if entry.endsWith("/") then continue; // Skip subgroups
        var found = false;
        for key in currentKeys {
          if entry == key {
            found = true;
            break;
          }
        }
        if !found {
          // Entry no longer in .env file - delete it
          const entryPath = groupPath + "/" + entry;
          if deleteEntry(dbPath, entryPath, password) {
            deleted += 1;
          }
        }
      }
    }

    return (added, updated, deleted);
  }

  // ============================================================================
  // SOPS File Ingestion
  // ============================================================================

  /*
   * Flatten a JSON object into dot-separated key-value pairs.
   *
   * Handles nested objects with dot notation (e.g., {"db":{"host":"x"}} becomes
   * [("db.host", "x")]). Arrays and non-object values are stored as their
   * JSON string representation.
   *
   * This is a simple parser for the flat/shallow JSON that sops -d produces.
   * Does not handle deeply nested structures or escaped quotes in values.
   *
   * :arg json: JSON string (output of sops -d --output-type json)
   * :returns: list of (key, value) string pairs
   */
  proc flattenSopsJson(json: string): list((string, string)) {
    var result: list((string, string));

    // Simple JSON parser for flat/shallow objects from sops
    // sops -d --output-type json typically produces {"key":"value",...}
    var s = json.strip();
    if s.size < 2 || s[0] != '{' then return result;

    // Remove outer braces
    s = s[1..#(s.size - 2)];

    // State machine to parse key:value pairs
    var i = 0;
    while i < s.size {
      // Skip whitespace and commas
      while i < s.size && (s[i] == ' ' || s[i] == '\n' || s[i] == '\r' ||
                            s[i] == '\t' || s[i] == ',') {
        i += 1;
      }
      if i >= s.size then break;

      // Parse key (must be a quoted string)
      if s[i] != '"' then break;
      i += 1;
      var key = "";
      while i < s.size && s[i] != '"' {
        if s[i] == '\\' && i + 1 < s.size {
          key += s[i+1];
          i += 2;
        } else {
          key += s[i];
          i += 1;
        }
      }
      if i < s.size then i += 1; // skip closing quote

      // Skip whitespace and colon
      while i < s.size && (s[i] == ' ' || s[i] == ':') do i += 1;

      if i >= s.size then break;

      // Parse value
      var value = "";
      if s[i] == '"' {
        // String value
        i += 1;
        while i < s.size && s[i] != '"' {
          if s[i] == '\\' && i + 1 < s.size {
            value += s[i+1];
            i += 2;
          } else {
            value += s[i];
            i += 1;
          }
        }
        if i < s.size then i += 1; // skip closing quote
        result.pushBack((key, value));
      } else if s[i] == '{' {
        // Nested object - collect and recursively flatten
        var depth = 1;
        var nested = "{";
        i += 1;
        while i < s.size && depth > 0 {
          if s[i] == '{' then depth += 1;
          else if s[i] == '}' then depth -= 1;
          if depth > 0 {
            nested += s[i];
          }
          i += 1;
        }
        nested += "}";
        // Recursively flatten with prefix
        const inner = flattenSopsJson(nested);
        for (innerKey, innerValue) in inner {
          result.pushBack((key + "." + innerKey, innerValue));
        }
      } else {
        // Number, boolean, null, or array - take as raw value
        var raw = "";
        var bracketDepth = 0;
        while i < s.size {
          if s[i] == '[' then bracketDepth += 1;
          else if s[i] == ']' then bracketDepth -= 1;
          else if bracketDepth == 0 && (s[i] == ',' || s[i] == '}') then break;
          raw += s[i];
          i += 1;
        }
        result.pushBack((key, raw.strip()));
      }
    }

    return result;
  }

  /*
   * Decrypt a SOPS-encrypted file and return its contents as key-value pairs.
   *
   * Uses: sops -d --output-type json <file>
   * The --output-type json flag ensures a consistent parseable format
   * regardless of source format (YAML, JSON, dotenv, INI).
   *
   * :arg filePath: Path to the SOPS-encrypted file
   * :returns: (success, list of (key, value) pairs)
   */
  proc decryptSopsFile(filePath: string): (bool, list((string, string))) {
    var empty: list((string, string));
    const sopsPath = getEnvOrDefault("REMOTE_JUGGLER_SOPS_PATH", "sops");

    try {
      var p = spawn([sopsPath, "-d", "--output-type", "json", filePath],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode != 0 {
        var errMsg: string;
        p.stderr.readAll(errMsg);
        verboseLog("KeePassXC decryptSopsFile: sops -d failed for ", filePath, ": ", errMsg.strip());
        return (false, empty);
      }

      var output: string;
      p.stdout.readAll(output);

      if output.strip() == "" {
        verboseLog("KeePassXC decryptSopsFile: empty output for ", filePath);
        return (false, empty);
      }

      const pairs = flattenSopsJson(output);
      return (true, pairs);
    } catch e {
      verboseLog("KeePassXC decryptSopsFile: Exception: ", e.message());
      return (false, empty);
    }
  }

  /*
   * Ingest a SOPS-encrypted file into the database.
   *
   * Decrypts with sops -d, stores each key-value pair under
   * RemoteJuggler/SOPS/{canonical-path}/{KEY}
   *
   * :arg dbPath: Path to the kdbx file
   * :arg sopsFilePath: Path to the SOPS-encrypted file
   * :arg password: Master password
   * :returns: (entriesAdded, entriesUpdated)
   */
  proc ingestSopsFile(dbPath: string, sopsFilePath: string, password: string): (int, int) {
    var added = 0;
    var updated = 0;

    // Decrypt
    const (ok, pairs) = decryptSopsFile(sopsFilePath);
    if !ok {
      verboseLog("KeePassXC ingestSopsFile: Failed to decrypt ", sopsFilePath);
      return (0, 0);
    }

    // Canonical path for group name
    const canonicalPath = sopsFilePath.replace("/", "_").replace("~", "home");
    const groupPath = "RemoteJuggler/SOPS/" + canonicalPath;

    // Ensure group exists
    try {
      var p = spawn(["keepassxc-cli", "mkdir", dbPath, groupPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();
    } catch { }

    // Store each key-value pair
    for (key, value) in pairs {
      if key == "" then continue;

      const entryPath = groupPath + "/" + key;

      // Check if entry already exists
      const (existsOk, existingValue) = getEntry(dbPath, entryPath, password);
      if existsOk {
        if existingValue != value {
          if setEntry(dbPath, entryPath, password, value) {
            updated += 1;
          }
        }
      } else {
        if setEntry(dbPath, entryPath, password, value) {
          added += 1;
        }
      }
    }

    return (added, updated);
  }

  /*
   * Sync a SOPS file with the database, detecting additions, updates, and deletions.
   *
   * Unlike ingestSopsFile which only adds/updates, this detects entries in the
   * database group that are no longer present in the SOPS file and deletes them.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg sopsFilePath: Path to the SOPS-encrypted file
   * :arg password: Master password
   * :returns: (added, updated, deleted) counts
   */
  proc syncSopsFile(dbPath: string, sopsFilePath: string, password: string): (int, int, int) {
    // First, do the normal ingest (adds + updates)
    const (added, updated) = ingestSopsFile(dbPath, sopsFilePath, password);

    // Decrypt to get current keys
    const (ok, pairs) = decryptSopsFile(sopsFilePath);
    if !ok {
      return (added, updated, 0);
    }

    var currentKeys: list(string);
    for (key, _) in pairs {
      if key != "" then currentKeys.pushBack(key);
    }

    // List entries currently in the database group
    const canonicalPath = sopsFilePath.replace("/", "_").replace("~", "home");
    const groupPath = "RemoteJuggler/SOPS/" + canonicalPath;
    const (listOk, entries) = listEntries(dbPath, groupPath, password);

    var deleted = 0;
    if listOk {
      for entry in entries {
        if entry.endsWith("/") then continue; // Skip subgroups
        var found = false;
        for key in currentKeys {
          if entry == key {
            found = true;
            break;
          }
        }
        if !found {
          const entryPath = groupPath + "/" + entry;
          if deleteEntry(dbPath, entryPath, password) {
            deleted += 1;
          }
        }
      }
    }

    return (added, updated, deleted);
  }

  /*
   * Export age public key from the key store for SOPS recipients configuration.
   *
   * Retrieves an age private key from KDBX and derives the public key
   * using age-keygen -y, suitable for .sops.yaml creation_rules.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg password: Master password
   * :arg group: Group to search for age key (default: "RemoteJuggler/SOPS")
   * :returns: (success, age public key string)
   */
  proc exportSopsAgeKey(dbPath: string, password: string,
                        group: string = "RemoteJuggler/SOPS"): (bool, string) {
    // Look for age key entry
    const ageKeyPath = group + "/age-key";
    const (ok, privateKey) = getEntry(dbPath, ageKeyPath, password);
    if !ok || privateKey == "" {
      verboseLog("KeePassXC exportSopsAgeKey: No age key found at ", ageKeyPath);
      return (false, "No age key found at " + ageKeyPath +
              ". Store an age private key with: keys store " + ageKeyPath + " <key>");
    }

    // Derive public key using age-keygen -y
    const ageKeygenPath = getEnvOrDefault("REMOTE_JUGGLER_AGE_KEYGEN_PATH", "age-keygen");
    try {
      var p = spawn([ageKeygenPath, "-y"],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.stdin.write(privateKey + "\n");
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        var pubKey: string;
        p.stdout.readAll(pubKey);
        return (true, pubKey.strip());
      } else {
        return (false, "age-keygen failed to derive public key");
      }
    } catch e {
      verboseLog("KeePassXC exportSopsAgeKey: Exception: ", e.message());
      return (false, "Failed to run age-keygen: " + e.message());
    }
  }

  // ============================================================================
  // Auto-Discovery
  // ============================================================================

  /*
   * Crawl directories for .env files and ingest each one.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg password: Master password
   * :arg rootDirs: Directories to search (defaults to ~, ~/git, ~/projects)
   * :returns: (filesFound, totalAdded, totalUpdated)
   */
  proc crawlEnvFiles(dbPath: string, password: string, rootDirs: list(string) = new list(string)): (int, int, int) {
    var dirs = rootDirs;
    if dirs.size == 0 {
      dirs.pushBack(expandTilde("~"));
      dirs.pushBack(expandTilde("~/git"));
      dirs.pushBack(expandTilde("~/projects"));
    }

    // Skip patterns
    const skipDirs = ["node_modules", ".git", ".venv", "target", "__pycache__",
                      ".cache", ".local", ".npm", ".cargo"];

    var filesFound = 0;
    var totalAdded = 0;
    var totalUpdated = 0;

    for rootDir in dirs {
      try {
        if !FileSystem.exists(rootDir) then continue;
      } catch { continue; }

      crawlEnvFilesRecursive(dbPath, password, rootDir, skipDirs,
                              filesFound, totalAdded, totalUpdated, 0);
    }

    return (filesFound, totalAdded, totalUpdated);
  }

  /* Recursive helper for crawlEnvFiles. Max depth 5 to avoid deep traversal. */
  proc crawlEnvFilesRecursive(dbPath: string, password: string, dir: string,
                               skipDirs: [] string,
                               ref filesFound: int, ref totalAdded: int,
                               ref totalUpdated: int, depth: int) {
    if depth > 5 then return;

    try {
      for entry in FileSystem.listDir(dir) {
        const fullPath = dir + "/" + entry;
        const isDir = FileSystem.isDir(fullPath);

        if isDir {
          // Check skip patterns
          var skip = false;
          for skipPattern in skipDirs {
            if entry == skipPattern { skip = true; break; }
          }
          if !skip {
            crawlEnvFilesRecursive(dbPath, password, fullPath, skipDirs,
                                    filesFound, totalAdded, totalUpdated, depth + 1);
          }
        } else {
          // Check if this is a .env file
          if isEnvFile(entry) {
            filesFound += 1;
            const (added, updated) = ingestEnvFile(dbPath, fullPath, password);
            totalAdded += added;
            totalUpdated += updated;
          }
          // Check if this is a SOPS-encrypted file
          else if isSopsReady() && isSopsFile(entry) {
            filesFound += 1;
            const (sopsAdded, sopsUpdated) = ingestSopsFile(dbPath, fullPath, password);
            totalAdded += sopsAdded;
            totalUpdated += sopsUpdated;
          }
        }
      }
    } catch { }
  }

  /* Check if a filename matches .env file patterns. */
  proc isEnvFile(name: string): bool {
    if name == ".env" then return true;
    if name.startsWith(".env.") then return true;   // .env.local, .env.production
    if name.endsWith(".env") then return true;       // app.env
    return false;
  }

  /*
   * Check if a filename matches SOPS-encrypted file patterns.
   *
   * Patterns recognized:
   *   *.sops.yaml, *.sops.yml, *.sops.json, *.sops.env,
   *   *.sops.ini, *.sops.toml
   *   secrets.enc.yaml, secrets.enc.yml, secrets.enc.json
   *   secrets.encrypted.yaml, secrets.encrypted.yml
   *
   * Excludes .sops.yaml (bare SOPS config file, not encrypted secrets).
   *
   * :arg name: File basename
   * :returns: true if matches SOPS-encrypted file patterns
   */
  proc isSopsFile(name: string): bool {
    // Bare .sops.yaml is the SOPS config file, not an encrypted file
    if name == ".sops.yaml" || name == ".sops.yml" then return false;

    // *.sops.{yaml,yml,json,env,ini,toml}
    if name.endsWith(".sops.yaml") then return true;
    if name.endsWith(".sops.yml") then return true;
    if name.endsWith(".sops.json") then return true;
    if name.endsWith(".sops.env") then return true;
    if name.endsWith(".sops.ini") then return true;
    if name.endsWith(".sops.toml") then return true;

    // secrets.enc.{yaml,yml,json}
    if name.endsWith(".enc.yaml") then return true;
    if name.endsWith(".enc.yml") then return true;
    if name.endsWith(".enc.json") then return true;

    // secrets.encrypted.{yaml,yml}
    if name.endsWith(".encrypted.yaml") then return true;
    if name.endsWith(".encrypted.yml") then return true;

    return false;
  }

  /*
   * Discover SSH keys in ~/.ssh/ and store metadata.
   *
   * Stores key type, fingerprint, and path (NOT the private key content).
   *
   * :arg dbPath: Path to the kdbx file
   * :arg password: Master password
   * :returns: Number of keys discovered
   */
  proc discoverSSHKeys(dbPath: string, password: string): int {
    const sshDir = expandTilde("~/.ssh");
    var discovered = 0;

    try {
      if !FileSystem.exists(sshDir) then return 0;
    } catch { return 0; }

    try {
      for entry in FileSystem.listDir(sshDir) {
        const fullPath = sshDir + "/" + entry;

        // Skip directories, public keys, known_hosts, config, authorized_keys
        if entry.endsWith(".pub") then continue;
        if entry == "known_hosts" || entry == "known_hosts.old" then continue;
        if entry == "config" then continue;
        if entry == "authorized_keys" then continue;

        try {
          if FileSystem.isDir(fullPath) then continue;
        } catch { continue; }

        // Check if it's a private key by reading first line
        var firstLine: string;
        try {
          var f = open(fullPath, ioMode.r);
          var reader = f.reader(locking=false);
          reader.readLine(firstLine);
          reader.close();
          f.close();
        } catch { continue; }

        if !firstLine.startsWith("-----BEGIN") then continue;

        // Get fingerprint via ssh-keygen
        var fingerprint = "";
        var keyType = "";
        try {
          var p = spawn(["ssh-keygen", "-l", "-f", fullPath],
                        stdout=pipeStyle.pipe, stderr=pipeStyle.close);
          p.wait();
          if p.exitCode == 0 {
            var output: string;
            p.stdout.readAll(output);
            fingerprint = output.strip();
            // Parse key type from fingerprint output (format: "bits fingerprint comment (type)")
            const parenPos = output.rfind("(");
            if parenPos >= 0 {
              keyType = output[parenPos+1..].strip().replace(")", "");
            }
          }
        } catch { }

        // Store metadata under RemoteJuggler/SSH/
        const entryPath = "RemoteJuggler/SSH/" + entry;
        const metadata = "path=" + fullPath + "\ntype=" + keyType + "\nfingerprint=" + fingerprint;
        if setEntry(dbPath, entryPath, password, metadata) {
          discovered += 1;
          verboseLog("KeePassXC: Discovered SSH key: ", entry, " (", keyType, ")");
        }
      }
    } catch { }

    return discovered;
  }

  /*
   * Discover credential-like environment variables and store them.
   *
   * Scans environment for variables matching credential patterns:
   * *_TOKEN, *_API_KEY, *_SECRET, *_PASSWORD, *_PASSPHRASE,
   * *_ACCESS_KEY, *_BEARER_TOKEN
   *
   * :arg dbPath: Path to the kdbx file
   * :arg password: Master password
   * :returns: Number of credentials discovered
   */
  proc discoverEnvCredentials(dbPath: string, password: string): int {
    var discovered = 0;

    const credPatterns = ["_TOKEN", "_API_KEY", "_SECRET", "_PASSWORD",
                          "_PASSPHRASE", "_ACCESS_KEY", "_BEARER_TOKEN",
                          "_PRIVATE_KEY"];

    // Use env command to list all environment variables
    try {
      var p = spawn(["env"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);

        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed == "" then continue;

          const eqPos = trimmed.find("=");
          if eqPos < 0 then continue;

          const varName = trimmed[0..#eqPos:int];
          const varValue = trimmed[eqPos+1..];

          // Check if variable name matches credential patterns
          const upperName = varName.toUpper();
          var isCredential = false;
          for pattern in credPatterns {
            if upperName.endsWith(pattern) {
              isCredential = true;
              break;
            }
          }

          if isCredential && varValue != "" {
            const entryPath = "RemoteJuggler/Discovered/" + varName;

            // Check if already stored
            const (existsOk, existingValue) = getEntry(dbPath, entryPath, password);
            if existsOk {
              if existingValue != varValue {
                if setEntry(dbPath, entryPath, password, varValue) {
                  discovered += 1;
                }
              }
            } else {
              if setEntry(dbPath, entryPath, password, varValue) {
                discovered += 1;
              }
            }
          }
        }
      }
    } catch { }

    return discovered;
  }

  // ============================================================================
  // Export
  // ============================================================================

  /*
   * Export entries from a group as .env file content.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg group: Group path to export (e.g., "RemoteJuggler/API")
   * :arg password: Master password
   * :arg format: Output format ("env" or "json")
   * :returns: (success, content)
   */
  proc exportEntries(dbPath: string, group: string, password: string,
                     format: string = "env"): (bool, string) {
    const (listOk, entries) = listEntries(dbPath, group, password);
    if !listOk {
      return (false, "");
    }

    if format == "json" {
      var jsonOutput = '{"group":"' + group + '","entries":[';
      var first = true;
      for entry in entries {
        if entry.endsWith("/") then continue; // Skip subgroups
        const entryPath = group + "/" + entry;
        const (found, value) = getEntry(dbPath, entryPath, password);
        if found {
          if !first then jsonOutput += ",";
          // Escape JSON strings
          const escapedValue = value.replace("\\", "\\\\").replace('"', '\\"');
          jsonOutput += '{"key":"' + entry + '","value":"' + escapedValue + '"}';
          first = false;
        }
      }
      jsonOutput += "]}";
      return (true, jsonOutput);
    } else {
      // .env format
      var envContent = "# Exported from KeePassXC group: " + group + "\n";
      for entry in entries {
        if entry.endsWith("/") then continue; // Skip subgroups
        const entryPath = group + "/" + entry;
        const (found, value) = getEntry(dbPath, entryPath, password);
        if found {
          // Quote values containing special characters
          if value.find(" ") != -1 || value.find("=") != -1 || value.find("#") != -1 {
            envContent += entry + '="' + value + '"\n';
          } else {
            envContent += entry + "=" + value + "\n";
          }
        }
      }
      return (true, envContent);
    }
  }

  // ============================================================================
  // Utility Functions
  // ============================================================================

  /*
   * Ensure a group exists in the database (create if missing).
   *
   * :arg dbPath: Path to the kdbx file
   * :arg group: Group path to create
   * :arg password: Master password
   */
  proc ensureGroup(dbPath: string, group: string, password: string) {
    try {
      var p = spawn(["keepassxc-cli", "mkdir", dbPath, group],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close, stderr=pipeStyle.close);
      p.stdin.write(password + "\n");
      p.stdin.close();
      p.wait();
    } catch { }
  }

  /*
   * Generate a random password of the given length.
   *
   * Uses /dev/urandom for entropy.
   *
   * :arg length: Password length
   * :returns: Random password string
   */
  proc generateRandomPassword(length: int): string {
    const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+";
    var password = "";

    try {
      var f = open("/dev/urandom", ioMode.r);
      var reader = f.reader(locking=false);

      for i in 0..<length {
        var byte: uint(8);
        reader.readBinary(byte);
        const idx = (byte % charset.size:uint(8)):int;
        password += charset[idx];
      }

      reader.close();
      f.close();
    } catch {
      // Fallback: use timestamp-based seed (less secure)
      use Time;
      var seed = dateTime.now().second * 1000 + dateTime.now().hour * 60;
      for i in 0..<length {
        seed = (seed * 1103515245 + 12345) % (2**31);
        const idx = (seed % charset.size):int;
        password += charset[idx:int];
      }
    }

    return password;
  }

  /*
   * Import existing credentials from RemoteJuggler config and environment.
   *
   * First imports well-known tokens from hardcoded mappings, then uses
   * pattern-based discovery for any remaining credential-like env vars.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg password: Master password
   * :returns: Number of entries imported
   */
  proc importExistingCredentials(dbPath: string, password: string): int {
    var imported = 0;

    // Import well-known tokens with specific paths
    const envTokens = [
      ("GITLAB_TOKEN", "RemoteJuggler/Tokens/GitLab/default"),
      ("GITHUB_TOKEN", "RemoteJuggler/Tokens/GitHub/default"),
      ("PERPLEXITY_API_KEY", "RemoteJuggler/API/PERPLEXITY_API_KEY"),
      ("BRAVE_API_KEY", "RemoteJuggler/API/BRAVE_API_KEY"),
      ("SHODAN_API_KEY", "RemoteJuggler/API/SHODAN_API_KEY"),
      ("ZAI_BEARER_TOKEN", "RemoteJuggler/API/ZAI_BEARER_TOKEN"),
      ("Z_AI_API_KEY", "RemoteJuggler/API/Z_AI_API_KEY")
    ];

    for (envName, entryPath) in envTokens {
      const val = getEnvVar(envName);
      if val != "" {
        if setEntry(dbPath, entryPath, password, val) {
          imported += 1;
          verboseLog("KeePassXC: Imported ", envName, " -> ", entryPath);
        }
      }
    }

    // Ensure Discovered group exists
    ensureGroup(dbPath, "RemoteJuggler/Discovered", password);

    // Also discover pattern-matched credential env vars
    imported += discoverEnvCredentials(dbPath, password);

    return imported;
  }

  // ============================================================================
  // Status
  // ============================================================================

  /*
   * Get comprehensive status of the KeePassXC integration.
   *
   * :returns: Status string suitable for display
   */
  proc getStatus(): string {
    var output = "KeePassXC Credential Authority Status\n";
    output += "=====================================\n\n";

    // CLI availability
    output += "keepassxc-cli: " + (if isAvailable() then "installed" else "NOT FOUND") + "\n";

    // Database
    const dbPath = getDatabasePath();
    output += "Database: " + dbPath + "\n";
    output += "  Exists: " + (if databaseExists() then "yes" else "no") + "\n";

    // HSM
    const hsmType = hsm_detect_available();
    output += "HSM: " + (if hsmType != HSM_TYPE_NONE then hsm_type_name(hsmType) else "none") + "\n";

    // Master password sealed
    const hasMaster = hsm_has_pin(KDBX_HSM_IDENTITY) != 0;
    output += "Master Password Sealed: " + (if hasMaster then "yes" else "no") + "\n";

    // YubiKey
    output += "YubiKey: " + (if isYubiKeyPresent() then "present" else "not detected") + "\n";

    // SOPS integration
    output += "SOPS: " + (if isSopsAvailable() then "installed" else "not found") + "\n";
    output += "age: " + (if isAgeAvailable() then "installed" else "not found") + "\n";

    // Auto-unlock
    output += "Auto-Unlock: " + (if canAutoUnlock() then "ready" else "not available") + "\n";

    // Entry count (if accessible)
    if canAutoUnlock() && databaseExists() {
      const (ok, password) = autoUnlock();
      if ok {
        const (listOk, entries) = listEntries(dbPath, "RemoteJuggler", password);
        if listOk {
          output += "Root Groups: " + entries.size:string + "\n";
        }
      }
    }

    return output;
  }

  // ============================================================================
  // IBSF Discovery Orchestrator
  // ============================================================================

  /*
   * Discover all secrets across all sources and return SecretEntry records.
   *
   * Orchestrates:
   *   1. KDBX group enumeration (API, Tokens, Infrastructure)
   *   2. Environment variable credential discovery
   *   3. SSH key metadata discovery
   *   4. .env file crawling
   *
   * Part of the Identity-Bound Secret Fabric (IBSF) Sprint B.
   *
   * :arg dbPath: Path to the kdbx file
   * :arg masterPw: KDBX master credential
   * :arg tcfsSecretsDir: Path to ~/tcfs/secrets/ (for sync status check)
   * :returns: (success, list of SecretEntry)
   */
  proc discoverAll(dbPath: string, masterPw: string,
                   tcfsSecretsDir: string = ""): (bool, list(Manifest.SecretEntry)) {
    var entries: list(Manifest.SecretEntry);
    const host = Manifest.getLocalHostname();
    const now = Manifest.nowISO8601();

    // 1. Enumerate KDBX groups
    const kdbxGroups = ["RemoteJuggler/API", "RemoteJuggler/Tokens",
                        "RemoteJuggler/Infrastructure", "RemoteJuggler/Discovered",
                        "RemoteJuggler/Environments"];
    for group in kdbxGroups {
      const (listOk, groupEntries) = listEntries(dbPath, group, masterPw);
      if listOk {
        for entryName in groupEntries {
          if entryName.endsWith("/") then continue;  // Skip subdirs
          if entryName.strip() == "" then continue;

          const entryPath = group + "/" + entryName;
          const (getOk, value) = getEntry(dbPath, entryPath, masterPw);
          if getOk && value != "" {
            const canonical = Manifest.deriveCanonicalName("kdbx", entryPath);
            const hash = Manifest.computeContentHash(value);
            const classification = Manifest.classifySecret(canonical);

            // Check sync status: does a corresponding file exist in tcfs?
            var synced = false;
            if tcfsSecretsDir != "" {
              // Check kdbx export dir
              const groupDirName = group.replace("/", "_");
              const exportPath = tcfsSecretsDir + "/kdbx/" + groupDirName + "/" + entryName;
              synced = exists(exportPath);
            }

            entries.pushBack(new Manifest.SecretEntry(
              canonical, "kdbx", entryPath, host, now, hash, synced, classification
            ));
          }
        }
      }
    }

    // 2. Environment variable credentials
    const credPatterns = ["_TOKEN", "_API_KEY", "_SECRET", "_PASSWORD",
                          "_PASSPHRASE", "_ACCESS_KEY", "_BEARER_TOKEN",
                          "_PRIVATE_KEY"];
    try {
      var p = spawn(["env"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();
      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed == "" then continue;
          const eqPos = trimmed.find("=");
          if eqPos < 0 then continue;

          const varName = trimmed[0..#eqPos:int];
          const varValue = trimmed[eqPos+1..];
          const upperName = varName.toUpper();

          var isCredential = false;
          for pattern in credPatterns {
            if upperName.endsWith(pattern) {
              isCredential = true;
              break;
            }
          }
          if isCredential && varValue != "" {
            const canonical = Manifest.deriveCanonicalName("env", varName);
            const hash = Manifest.computeContentHash(varValue);
            const classification = Manifest.classifySecret(varName);

            entries.pushBack(new Manifest.SecretEntry(
              canonical, "env", varName, host, now, hash, false, classification
            ));
          }
        }
      }
    } catch { }

    // 3. SSH key metadata
    const sshDir = expandTilde("~/.ssh");
    if isDir(sshDir) {
      try {
        for entry in listDir(sshDir, dirs=false) {
          const fullPath = sshDir + "/" + entry;
          // Check if it's a private key (has matching .pub)
          if exists(fullPath + ".pub") {
            const canonical = Manifest.deriveCanonicalName("ssh", fullPath);

            // Hash the public key fingerprint, not the private key
            var hash = "";
            try {
              var fp = spawn(["ssh-keygen", "-l", "-f", fullPath],
                             stdout=pipeStyle.pipe, stderr=pipeStyle.close);
              fp.wait();
              if fp.exitCode == 0 {
                var fpLine: string;
                if fp.stdout.readLine(fpLine) {
                  hash = Manifest.computeContentHash(fpLine.strip());
                }
              }
            } catch { }

            entries.pushBack(new Manifest.SecretEntry(
              canonical, "ssh", fullPath, host, now, hash, false, "personal"
            ));
          }
        }
      } catch { }
    }

    return (true, entries);
  }
}

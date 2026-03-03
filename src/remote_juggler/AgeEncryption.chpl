/*
 * AgeEncryption.chpl - Identity-Based Encryption for IBSF (Sprint C)
 *
 * Part of RemoteJuggler - Backend-agnostic git identity management
 *
 * Provides age encryption/decryption for secrets synced via TCFS.
 * Wraps the `age` CLI and platform-specific age plugins:
 *   - macOS: age-plugin-yubikey (PIV-backed P-256, key never leaves YubiKey)
 *   - Linux: age-plugin-tpm (TPM 2.0 sealed P-256, unattended decryption)
 *   - Fallback: standard age X25519 identity file
 *
 * Design decision: Two-plugin strategy chosen over ssh-to-age (can't access
 * hardware keys) and gpg2age (experimental, can't use smartcard GPG keys).
 * See docs/IDENTITY_BOUND_SECRET_FABRIC.md Sprint C for rationale.
 *
 * :author: RemoteJuggler Team
 * :version: 2.0.0
 * :license: MIT
 */
prototype module AgeEncryption {
  use IO;
  use List;
  use FileSystem;
  use Path;
  use Subprocess;
  public use super.Core;

  // ==========================================================================
  // Constants
  // ==========================================================================

  // File extension for age-encrypted files
  const AGE_EXTENSION = ".age";

  // Age plugin binary names
  const AGE_PLUGIN_YUBIKEY = "age-plugin-yubikey";
  const AGE_PLUGIN_TPM = "age-plugin-tpm";

  // Identity file locations
  const AGE_IDENTITY_DIR = ".config/remotejuggler/age";
  const AGE_PUBKEY_FILE = "recipient.txt";
  const AGE_IDENTITY_FILE = "identity.txt";

  // ==========================================================================
  // Types
  // ==========================================================================

  /*
   * Available age key backend types.
   */
  enum AgeBackend {
    yubikey,        // age-plugin-yubikey (macOS/Linux with YubiKey)
    tpm,            // age-plugin-tpm (Linux TPM 2.0)
    identity_file,  // Standard age X25519 identity file
    noBackend       // No backend available
  }

  /*
   * Result of an encryption or decryption operation.
   */
  record AgeResult {
    var ok: bool;
    var outputPath: string;
    var errMsg: string;

    proc init() {
      this.ok = false;
      this.outputPath = "";
      this.errMsg = "";
    }

    proc init(ok: bool, outputPath: string, errMsg: string = "") {
      this.ok = ok;
      this.outputPath = outputPath;
      this.errMsg = errMsg;
    }
  }

  // ==========================================================================
  // Backend Detection
  // ==========================================================================

  /*
   * Detect the best available age backend for this platform.
   *
   * Priority:
   *   1. age-plugin-yubikey (if YubiKey present)
   *   2. age-plugin-tpm (if TPM 2.0 available, Linux only)
   *   3. Standard identity file (if exists)
   *   4. none
   *
   * :returns: Best available AgeBackend
   */
  proc detectAgeBackend(): AgeBackend {
    // Check for age-plugin-yubikey
    if hasBinary(AGE_PLUGIN_YUBIKEY) {
      // Verify a YubiKey identity exists
      const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
      const ykIdentity = idDir + "/yubikey-identity.txt";
      if exists(ykIdentity) {
        return AgeBackend.yubikey;
      }
      // Even without pre-generated identity, plugin is available
      // for key generation
      return AgeBackend.yubikey;
    }

    // Check for age-plugin-tpm
    if hasBinary(AGE_PLUGIN_TPM) {
      const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
      const tpmIdentity = idDir + "/tpm-identity.txt";
      if exists(tpmIdentity) {
        return AgeBackend.tpm;
      }
      return AgeBackend.tpm;
    }

    // Check for standard identity file
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const stdIdentity = idDir + "/" + AGE_IDENTITY_FILE;
    if exists(stdIdentity) {
      return AgeBackend.identity_file;
    }

    return AgeBackend.noBackend;
  }

  /*
   * Check if the `age` CLI binary is available.
   *
   * :returns: true if age is on PATH
   */
  proc hasAgeBinary(): bool {
    return hasBinary("age");
  }

  /*
   * Check if a binary is on PATH.
   */
  private proc hasBinary(name: string): bool {
    try {
      var p = spawn(["which", name],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();
      return p.exitCode == 0;
    } catch {
      return false;
    }
  }

  // ==========================================================================
  // Key Management
  // ==========================================================================

  /*
   * Get the age recipient (public key) for the current identity.
   *
   * Returns the public key string (age1... or age1yubikey1... or age1tpm1...)
   * that can be used for encryption.
   *
   * :returns: (success, recipientString)
   */
  proc getAgeRecipient(): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const pubkeyFile = idDir + "/" + AGE_PUBKEY_FILE;

    // Try cached recipient file first
    if exists(pubkeyFile) {
      try {
        var f = open(pubkeyFile, ioMode.r);
        var r = f.reader(locking=false);
        var line: string;
        while r.readLine(line) {
          const trimmed = line.strip();
          if trimmed != "" && !trimmed.startsWith("#") {
            return (true, trimmed);
          }
        }
      } catch { }
    }

    // Try to derive from backend
    const backend = detectAgeBackend();
    select backend {
      when AgeBackend.yubikey {
        return deriveYubiKeyRecipient();
      }
      when AgeBackend.tpm {
        return deriveTPMRecipient();
      }
      when AgeBackend.identity_file {
        return deriveIdentityFileRecipient();
      }
      otherwise {
        return (false, "");
      }
    }
  }

  /*
   * Get the path to the age identity file for decryption.
   *
   * :returns: (success, identityFilePath)
   */
  proc getAgeIdentityPath(): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const backend = detectAgeBackend();

    select backend {
      when AgeBackend.yubikey {
        const path = idDir + "/yubikey-identity.txt";
        if exists(path) then return (true, path);
      }
      when AgeBackend.tpm {
        const path = idDir + "/tpm-identity.txt";
        if exists(path) then return (true, path);
      }
      when AgeBackend.identity_file {
        const path = idDir + "/" + AGE_IDENTITY_FILE;
        if exists(path) then return (true, path);
      }
      otherwise { }
    }
    return (false, "");
  }

  /*
   * Initialize age key infrastructure for the current platform.
   *
   * On macOS with YubiKey: generates a PIV-backed age identity
   * On Linux with TPM: generates a TPM-sealed age identity
   * Fallback: generates a standard age X25519 identity (less secure)
   *
   * :arg backend: Which backend to use (auto-detect if not specified)
   * :returns: (success, recipientPublicKey)
   */
  proc initAgeKeys(backend: AgeBackend = AgeBackend.noBackend): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    ensureDirectory(idDir);

    var useBackend = backend;
    if useBackend == AgeBackend.noBackend {
      useBackend = detectAgeBackend();
    }

    select useBackend {
      when AgeBackend.yubikey {
        return initYubiKeyAge(idDir);
      }
      when AgeBackend.tpm {
        return initTPMAge(idDir);
      }
      otherwise {
        return initIdentityFileAge(idDir);
      }
    }
  }

  /*
   * Initialize YubiKey-backed age identity.
   * Uses age-plugin-yubikey to generate a PIV key on the YubiKey.
   */
  private proc initYubiKeyAge(idDir: string): (bool, string) {
    const identityPath = idDir + "/yubikey-identity.txt";
    const recipientPath = idDir + "/" + AGE_PUBKEY_FILE;

    // If identity already exists, just read the recipient
    if exists(identityPath) {
      const (ok, recipient) = deriveYubiKeyRecipient();
      if ok then return (true, recipient);
    }

    // Generate new YubiKey age identity
    // age-plugin-yubikey generates to stdout
    try {
      var p = spawn([AGE_PLUGIN_YUBIKEY, "--generate",
                     "--touch-policy", "cached",
                     "--pin-policy", "once"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);

        // Write identity file
        {
          var f = open(identityPath, ioMode.cw);
          var w = f.writer(locking=false);
          w.write(output);
        }
        // Set secure permissions
        chmod(identityPath, 0o600: int);

        // Extract recipient from identity output
        // Lines starting with "# recipient:" or "age1yubikey1..."
        var recipient = "";
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed.startsWith("# recipient: ") {
            recipient = trimmed["# recipient: ".size..];
            break;
          }
          if trimmed.startsWith("age1yubikey1") {
            recipient = trimmed;
            break;
          }
        }

        if recipient != "" {
          // Cache recipient to pubkey file
          {
            var f = open(recipientPath, ioMode.cw);
            var w = f.writer(locking=false);
            w.writeln("# YubiKey age recipient — do not edit");
            w.writeln(recipient);
          }
          return (true, recipient);
        }
      }

      // Read stderr for error message
      var errOutput: string;
      p.stderr.readAll(errOutput);
      return (false, "");
    } catch {
      return (false, "");
    }
  }

  /*
   * Initialize TPM-backed age identity.
   * Uses age-plugin-tpm to generate a TPM-sealed key.
   */
  private proc initTPMAge(idDir: string): (bool, string) {
    const identityPath = idDir + "/tpm-identity.txt";
    const recipientPath = idDir + "/" + AGE_PUBKEY_FILE;

    if exists(identityPath) {
      const (ok, recipient) = deriveTPMRecipient();
      if ok then return (true, recipient);
    }

    try {
      var p = spawn([AGE_PLUGIN_TPM, "--generate"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);

        {
          var f = open(identityPath, ioMode.cw);
          var w = f.writer(locking=false);
          w.write(output);
        }
        chmod(identityPath, 0o600: int);

        // Extract recipient
        var recipient = "";
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed.startsWith("# recipient: ") {
            recipient = trimmed["# recipient: ".size..];
            break;
          }
          if trimmed.startsWith("age1tpm") {
            recipient = trimmed;
            break;
          }
        }

        if recipient != "" {
          {
            var f = open(recipientPath, ioMode.cw);
            var w = f.writer(locking=false);
            w.writeln("# TPM age recipient — do not edit");
            w.writeln(recipient);
          }
          return (true, recipient);
        }
      }
      return (false, "");
    } catch {
      return (false, "");
    }
  }

  /*
   * Initialize standard age identity file (software key, fallback).
   */
  private proc initIdentityFileAge(idDir: string): (bool, string) {
    const identityPath = idDir + "/" + AGE_IDENTITY_FILE;
    const recipientPath = idDir + "/" + AGE_PUBKEY_FILE;

    if exists(identityPath) {
      const (ok, recipient) = deriveIdentityFileRecipient();
      if ok then return (true, recipient);
    }

    if !hasAgeBinary() {
      return (false, "");
    }

    try {
      var p = spawn(["age-keygen"],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);

        {
          var f = open(identityPath, ioMode.cw);
          var w = f.writer(locking=false);
          w.write(output);
        }
        chmod(identityPath, 0o600: int);

        // Extract public key from "# public key: age1..."
        var recipient = "";
        for line in output.split("\n") {
          const trimmed = line.strip();
          if trimmed.startsWith("# public key: ") {
            recipient = trimmed["# public key: ".size..];
            break;
          }
        }

        if recipient != "" {
          {
            var f = open(recipientPath, ioMode.cw);
            var w = f.writer(locking=false);
            w.writeln("# age recipient (software key) — do not edit");
            w.writeln(recipient);
          }
          return (true, recipient);
        }
      }
      return (false, "");
    } catch {
      return (false, "");
    }
  }

  // ==========================================================================
  // Recipient Derivation Helpers
  // ==========================================================================

  private proc deriveYubiKeyRecipient(): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const identityPath = idDir + "/yubikey-identity.txt";
    return extractRecipientFromFile(identityPath, "age1yubikey1");
  }

  private proc deriveTPMRecipient(): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const identityPath = idDir + "/tpm-identity.txt";
    return extractRecipientFromFile(identityPath, "age1tpm");
  }

  private proc deriveIdentityFileRecipient(): (bool, string) {
    const idDir = expandTilde("~/" + AGE_IDENTITY_DIR);
    const identityPath = idDir + "/" + AGE_IDENTITY_FILE;
    return extractRecipientFromFile(identityPath, "age1");
  }

  /*
   * Extract age recipient public key from an identity file.
   * Looks for "# recipient: <key>" or a line starting with the prefix.
   */
  private proc extractRecipientFromFile(path: string,
                                         prefix: string): (bool, string) {
    if !exists(path) then return (false, "");

    try {
      var f = open(path, ioMode.r);
      var r = f.reader(locking=false);
      var line: string;
      while r.readLine(line) {
        const trimmed = line.strip();
        if trimmed.startsWith("# recipient: ") {
          return (true, trimmed["# recipient: ".size..]);
        }
        // Some formats put the recipient as a bare line
        if trimmed.startsWith(prefix) && !trimmed.startsWith("#") {
          // Could be the identity private key line; skip AGE-SECRET-KEY
          if !trimmed.startsWith("AGE-SECRET-KEY-") {
            return (true, trimmed);
          }
        }
      }
    } catch { }

    // Fallback: check the cached recipient file
    const dir = dirname(path);
    const recipientPath = dir + "/" + AGE_PUBKEY_FILE;
    if exists(recipientPath) && recipientPath != path {
      try {
        var f = open(recipientPath, ioMode.r);
        var r = f.reader(locking=false);
        var line: string;
        while r.readLine(line) {
          const trimmed = line.strip();
          if trimmed != "" && !trimmed.startsWith("#") {
            return (true, trimmed);
          }
        }
      } catch { }
    }

    return (false, "");
  }

  // ==========================================================================
  // Encryption
  // ==========================================================================

  /*
   * Encrypt a file using the user's age recipient key.
   *
   * Produces <inputPath>.age as output (or custom outputPath).
   * The original file is NOT deleted.
   *
   * :arg inputPath: Path to plaintext file
   * :arg outputPath: Path for encrypted output (default: inputPath + ".age")
   * :arg recipient: Explicit recipient key (default: auto-detect from config)
   * :returns: AgeResult with success status and output path
   */
  proc ageEncryptFile(inputPath: string,
                      outputPath: string = "",
                      recipient: string = ""): AgeResult {
    if !hasAgeBinary() {
      return new AgeResult(false, "", "age binary not found on PATH");
    }

    if !exists(inputPath) {
      return new AgeResult(false, "", "Input file does not exist: " + inputPath);
    }

    // Resolve recipient
    var recipientKey = recipient;
    if recipientKey == "" {
      const (ok, detected) = getAgeRecipient();
      if !ok {
        return new AgeResult(false, "",
          "No age recipient key configured. Run `remote-juggler age init` first.");
      }
      recipientKey = detected;
    }

    // Resolve output path
    var outPath = outputPath;
    if outPath == "" {
      outPath = inputPath + AGE_EXTENSION;
    }

    // Run age encrypt
    try {
      var p = spawn(["age", "-r", recipientKey, "-o", outPath, inputPath],
                    stdout=pipeStyle.close, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode == 0 {
        return new AgeResult(true, outPath);
      }

      var errOutput: string;
      p.stderr.readAll(errOutput);
      return new AgeResult(false, "", "age encrypt failed: " + errOutput.strip());
    } catch e {
      return new AgeResult(false, "", "age encrypt error: " + e.message());
    }
  }

  /*
   * Encrypt a string value and write the ciphertext to a file.
   *
   * :arg value: Plaintext string to encrypt
   * :arg outputPath: Path for encrypted output
   * :arg recipient: Explicit recipient key (default: auto-detect)
   * :returns: AgeResult
   */
  proc ageEncryptValue(value: string,
                       outputPath: string,
                       recipient: string = ""): AgeResult {
    if !hasAgeBinary() {
      return new AgeResult(false, "", "age binary not found on PATH");
    }

    var recipientKey = recipient;
    if recipientKey == "" {
      const (ok, detected) = getAgeRecipient();
      if !ok {
        return new AgeResult(false, "",
          "No age recipient key configured.");
      }
      recipientKey = detected;
    }

    // Pipe value to age via stdin
    try {
      var p = spawn(["age", "-r", recipientKey, "-o", outputPath],
                    stdin=pipeStyle.pipe, stdout=pipeStyle.close,
                    stderr=pipeStyle.pipe);
      p.stdin.write(value);
      p.stdin.close();
      p.wait();

      if p.exitCode == 0 {
        return new AgeResult(true, outputPath);
      }

      var errOutput: string;
      p.stderr.readAll(errOutput);
      return new AgeResult(false, "", "age encrypt failed: " + errOutput.strip());
    } catch e {
      return new AgeResult(false, "", "age encrypt error: " + e.message());
    }
  }

  // ==========================================================================
  // Decryption
  // ==========================================================================

  /*
   * Decrypt an age-encrypted file using the user's identity.
   *
   * :arg inputPath: Path to .age encrypted file
   * :arg outputPath: Path for decrypted output (default: strip .age extension)
   * :returns: AgeResult
   */
  proc ageDecryptFile(inputPath: string,
                      outputPath: string = ""): AgeResult {
    if !hasAgeBinary() {
      return new AgeResult(false, "", "age binary not found on PATH");
    }

    if !exists(inputPath) {
      return new AgeResult(false, "", "Encrypted file does not exist: " + inputPath);
    }

    const (idOk, identityPath) = getAgeIdentityPath();
    if !idOk {
      return new AgeResult(false, "",
        "No age identity found. Run `remote-juggler age init` first.");
    }

    var outPath = outputPath;
    if outPath == "" {
      // Strip .age extension
      if inputPath.endsWith(AGE_EXTENSION) {
        outPath = inputPath[0..#(inputPath.size - AGE_EXTENSION.size)];
      } else {
        outPath = inputPath + ".dec";
      }
    }

    try {
      var p = spawn(["age", "-d", "-i", identityPath, "-o", outPath, inputPath],
                    stdout=pipeStyle.close, stderr=pipeStyle.pipe);
      p.wait();

      if p.exitCode == 0 {
        return new AgeResult(true, outPath);
      }

      var errOutput: string;
      p.stderr.readAll(errOutput);
      return new AgeResult(false, "", "age decrypt failed: " + errOutput.strip());
    } catch e {
      return new AgeResult(false, "", "age decrypt error: " + e.message());
    }
  }

  /*
   * Decrypt an age-encrypted file and return the plaintext value.
   *
   * :arg inputPath: Path to .age encrypted file
   * :returns: (success, plaintextValue)
   */
  proc ageDecryptToString(inputPath: string): (bool, string) {
    if !hasAgeBinary() {
      return (false, "");
    }

    if !exists(inputPath) {
      return (false, "");
    }

    const (idOk, identityPath) = getAgeIdentityPath();
    if !idOk {
      return (false, "");
    }

    try {
      var p = spawn(["age", "-d", "-i", identityPath, inputPath],
                    stdout=pipeStyle.pipe, stderr=pipeStyle.close);
      p.wait();

      if p.exitCode == 0 {
        var output: string;
        p.stdout.readAll(output);
        return (true, output);
      }
    } catch { }

    return (false, "");
  }

  // ==========================================================================
  // Re-keying
  // ==========================================================================

  /*
   * Re-encrypt all .age files in a directory with a new recipient.
   *
   * Decrypts each file with the current identity, then re-encrypts
   * with the new recipient. Used after identity key rotation.
   *
   * :arg dirPath: Directory containing .age files
   * :arg newRecipient: New age recipient public key
   * :arg recursive: Whether to recurse into subdirectories
   * :returns: (totalFiles, successCount, failCount)
   */
  proc ageRekeyDirectory(dirPath: string,
                         newRecipient: string,
                         recursive: bool = true): (int, int, int) {
    var total = 0;
    var success = 0;
    var failed = 0;

    if !isDir(dirPath) {
      return (0, 0, 0);
    }

    try {
      for entry in listDir(dirPath, dirs=true) {
        const fullPath = dirPath + "/" + entry;

        if isDir(fullPath) && recursive {
          const (subTotal, subSuccess, subFailed) =
            ageRekeyDirectory(fullPath, newRecipient, recursive);
          total += subTotal;
          success += subSuccess;
          failed += subFailed;
          continue;
        }

        if !entry.endsWith(AGE_EXTENSION) then continue;
        total += 1;

        // Decrypt with current identity
        const (decOk, plaintext) = ageDecryptToString(fullPath);
        if !decOk {
          failed += 1;
          continue;
        }

        // Re-encrypt with new recipient to temp file, then rename atomically
        const tmpPath = fullPath + ".tmp";
        const result = ageEncryptValue(plaintext, tmpPath, newRecipient);
        if result.ok {
          try {
            rename(tmpPath, fullPath);
            success += 1;
          } catch {
            // Rename failed — clean up temp file and count as failure
            try { remove(tmpPath); } catch { }
            failed += 1;
          }
        } else {
          // Encryption failed — clean up temp file if it was created
          try { remove(tmpPath); } catch { }
          failed += 1;
        }
      }
    } catch {
      // Directory listing failed
    }

    return (total, success, failed);
  }

  // ==========================================================================
  // Utility
  // ==========================================================================

  /*
   * Check if a file is age-encrypted (has .age extension).
   */
  proc isAgeEncrypted(path: string): bool {
    return path.endsWith(AGE_EXTENSION);
  }

  /*
   * Get the plaintext path for an encrypted file (strip .age).
   */
  proc plaintextPath(agePath: string): string {
    if agePath.endsWith(AGE_EXTENSION) {
      return agePath[0..#(agePath.size - AGE_EXTENSION.size)];
    }
    return agePath;
  }

  /*
   * Get the encrypted path for a plaintext file (append .age).
   */
  proc encryptedPath(plainPath: string): string {
    if plainPath.endsWith(AGE_EXTENSION) {
      return plainPath;
    }
    return plainPath + AGE_EXTENSION;
  }

  /*
   * Get a human-readable status string for the age encryption subsystem.
   */
  proc ageStatusString(): string {
    const backend = detectAgeBackend();
    const hasAge = hasAgeBinary();
    const (hasRecipient, recipient) = getAgeRecipient();

    var status = "Age Encryption: ";
    if !hasAge {
      return status + "NOT AVAILABLE (age binary not found)";
    }

    select backend {
      when AgeBackend.yubikey {
        status += "YubiKey PIV (age-plugin-yubikey)";
      }
      when AgeBackend.tpm {
        status += "TPM 2.0 (age-plugin-tpm)";
      }
      when AgeBackend.identity_file {
        status += "Software identity file";
      }
      when AgeBackend.noBackend {
        status += "No identity configured";
      }
    }

    if hasRecipient {
      // Show first 20 chars of recipient for identification
      const display = if recipient.size > 20
                      then recipient[0..#20] + "..."
                      else recipient;
      status += " | Recipient: " + display;
    }

    return status;
  }

  /*
   * Ensure a directory exists with secure permissions.
   */
  private proc ensureDirectory(path: string) {
    if !exists(path) {
      try {
        mkdir(path, parents=true);
        chmod(path, 0o700: int);
      } catch { }
    }
  }

  /*
   * Set file permissions (chmod wrapper).
   */
  private proc chmod(path: string, mode: int) {
    try {
      spawn(["chmod", mode:string, path]).wait();
    } catch { }
  }
}

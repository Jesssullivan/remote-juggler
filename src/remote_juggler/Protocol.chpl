/*
 * Protocol.chpl - JSON-RPC 2.0 Message Handling for RemoteJuggler MCP Server
 *
 * This module provides the core JSON-RPC 2.0 protocol primitives for MCP/ACP
 * communication. All message parsing and generation is handled here.
 *
 * JSON-RPC 2.0 Specification: https://www.jsonrpc.org/specification
 * MCP Protocol Version: 2025-11-25
 */
prototype module Protocol {
  use IO;
  use JSON;
  use List;

  // ============================================================================
  // JSON-RPC 2.0 Error Codes (Standard)
  // ============================================================================

  // Parse error: Invalid JSON was received by the server
  const PARSE_ERROR: int = -32700;

  // Invalid Request: The JSON sent is not a valid Request object
  const INVALID_REQUEST: int = -32600;

  // Method not found: The method does not exist / is not available
  const METHOD_NOT_FOUND: int = -32601;

  // Invalid params: Invalid method parameter(s)
  const INVALID_PARAMS: int = -32602;

  // Internal error: Internal JSON-RPC error
  const INTERNAL_ERROR: int = -32603;

  // ============================================================================
  // Application-Specific Error Codes (1000-1999)
  // ============================================================================

  // Identity not found in configuration
  const IDENTITY_NOT_FOUND: int = 1001;

  // SSH key validation or connectivity failed
  const SSH_VALIDATION_FAILED: int = 1002;

  // Git operation (remote, config) failed
  const GIT_OPERATION_FAILED: int = 1003;

  // Configuration file error (parse, missing, permissions)
  const CONFIG_ERROR: int = 1004;

  // Keychain operation failed (macOS Security.framework)
  const KEYCHAIN_ERROR: int = 1005;

  // GPG operation failed
  const GPG_ERROR: int = 1006;

  // Provider CLI (glab/gh) not available or failed
  const PROVIDER_CLI_ERROR: int = 1007;

  // ============================================================================
  // JSON-RPC Message Types
  // ============================================================================

  /*
   * JsonRpcRequest - Incoming request from client
   *
   * Per JSON-RPC 2.0:
   * - jsonrpc: MUST be exactly "2.0"
   * - id: Identifier established by client (int, string, or null for notifications)
   * - method: String containing the name of the method to be invoked
   * - params: Structured value holding parameter values (optional)
   */
  record JsonRpcRequest {
    var jsonrpc: string = "2.0";
    var id: int = -1;           // -1 indicates no ID (notification)
    var method: string = "";
    var params: string = "{}";  // Raw JSON string for flexibility
  }

  /*
   * JsonRpcResponse - Successful response to client
   *
   * Per JSON-RPC 2.0:
   * - jsonrpc: MUST be exactly "2.0"
   * - id: Same value as the request id
   * - result: Value determined by method invoked on the server
   */
  record JsonRpcResponse {
    var jsonrpc: string = "2.0";
    var id: int;
    var result: string;  // JSON-encoded result
  }

  /*
   * JsonRpcErrorData - Error object for error responses
   *
   * Per JSON-RPC 2.0:
   * - code: Number indicating error type
   * - message: Short description of the error
   * - data: Additional information (optional, omitted here for simplicity)
   */
  record JsonRpcErrorData {
    var code: int;
    var message: string;
  }

  /*
   * JsonRpcError - Error response to client
   *
   * Per JSON-RPC 2.0:
   * - jsonrpc: MUST be exactly "2.0"
   * - id: Same value as request id (or null if error in detecting id)
   * - error: Error object
   */
  record JsonRpcErrorResponse {
    var jsonrpc: string = "2.0";
    var id: int;
    var error: JsonRpcErrorData;
  }

  // ============================================================================
  // JSON Escaping Utilities
  // ============================================================================

  /*
   * Escape a string for safe inclusion in JSON output.
   * Handles: backslash, quotes, newlines, tabs, carriage returns.
   * Strips NUL bytes and other control characters (U+0000..U+001F)
   * that corrupt JSON-RPC when passed through subprocess output.
   */
  proc escapeJsonString(s: string): string {
    var result: string = "";
    for ch in s {
      select ch {
        when '"' do result += '\\"';
        when '\\' do result += "\\\\";
        when '\n' do result += "\\n";
        when '\r' do result += "\\r";
        when '\t' do result += "\\t";
        otherwise {
          // Strip NUL bytes and other ASCII control characters (< 0x20).
          // These come from C string terminators in subprocess readAll()
          // and corrupt JSON-RPC responses sent through the gateway proxy.
          if ch.numBytes == 1 && ch.byte(0) < 0x20 {
            // Skip control character.
          } else {
            result += ch;
          }
        }
      }
    }
    return result;
  }

  /*
   * Unescape a JSON string value.
   * Reverses escapeJsonString operations.
   */
  proc unescapeJsonString(s: string): string {
    var result: string = "";
    var i = 0;
    while i < s.size {
      if s[i] == '\\' && i + 1 < s.size {
        select s[i + 1] {
          when '"' { result += '"'; i += 2; }
          when '\\' { result += '\\'; i += 2; }
          when 'n' { result += '\n'; i += 2; }
          when 'r' { result += '\r'; i += 2; }
          when 't' { result += '\t'; i += 2; }
          otherwise { result += s[i]; i += 1; }
        }
      } else {
        result += s[i];
        i += 1;
      }
    }
    return result;
  }

  // ============================================================================
  // Manual JSON Parsing Helpers
  // ============================================================================

  /*
   * Extract a string value from a JSON object by key.
   * Returns (found, value) tuple.
   *
   * Note: This is a simple parser for flat JSON objects. For nested objects,
   * the raw string is returned for params fields.
   */
  proc extractJsonString(json: string, key: string): (bool, string) {
    // Look for "key": "value" pattern
    const searchKey = '"' + key + '":';
    const keyPos = json.find(searchKey);

    if keyPos == -1 {
      return (false, "");
    }

    // Find the start of the value (skip whitespace after colon)
    var valueStart = keyPos + searchKey.size;
    while valueStart < json.size && (json[valueStart] == ' ' || json[valueStart] == '\t') {
      valueStart += 1;
    }

    if valueStart >= json.size {
      return (false, "");
    }

    // Check if it's a string value (starts with quote)
    if json[valueStart] == '"' {
      valueStart += 1;  // Skip opening quote

      // Find closing quote (handling escapes)
      var valueEnd = valueStart;
      var escaped = false;
      while valueEnd < json.size {
        if escaped {
          escaped = false;
          valueEnd += 1;
        } else if json[valueEnd] == '\\' {
          escaped = true;
          valueEnd += 1;
        } else if json[valueEnd] == '"' {
          break;
        } else {
          valueEnd += 1;
        }
      }

      const rawValue = json[valueStart..<valueEnd];
      return (true, unescapeJsonString(rawValue));
    }

    return (false, "");
  }

  /*
   * Extract an integer value from a JSON object by key.
   * Returns (found, value) tuple.
   */
  proc extractJsonInt(json: string, key: string): (bool, int) {
    const searchKey = '"' + key + '":';
    const keyPos = json.find(searchKey);

    if keyPos == -1 {
      return (false, 0);
    }

    // Find the start of the value
    var valueStart = keyPos + searchKey.size;
    while valueStart < json.size && (json[valueStart] == ' ' || json[valueStart] == '\t') {
      valueStart += 1;
    }

    if valueStart >= json.size {
      return (false, 0);
    }

    // Check for null
    if valueStart + 4 <= json.size && json[valueStart..#4] == "null" {
      return (true, -1);  // Use -1 to represent null ID
    }

    // Parse integer
    var valueEnd = valueStart;
    var isNegative = false;

    if json[valueStart] == '-' {
      isNegative = true;
      valueStart += 1;
      valueEnd += 1;
    }

    while valueEnd < json.size &&
          json[valueEnd] >= '0' && json[valueEnd] <= '9' {
      valueEnd += 1;
    }

    if valueEnd == valueStart {
      return (false, 0);
    }

    const numStr = json[valueStart..<valueEnd];
    try {
      var value = numStr: int;
      if isNegative then value = -value;
      return (true, value);
    } catch {
      return (false, 0);
    }
  }

  /*
   * Extract a nested JSON object by key.
   * Returns the raw JSON string for the object value.
   */
  proc extractJsonObject(json: string, key: string): (bool, string) {
    const searchKey = '"' + key + '":';
    const keyPos = json.find(searchKey);

    if keyPos == -1 {
      return (false, "{}");
    }

    // Find the start of the value
    var valueStart = keyPos + searchKey.size;
    while valueStart < json.size && (json[valueStart] == ' ' || json[valueStart] == '\t') {
      valueStart += 1;
    }

    if valueStart >= json.size {
      return (false, "{}");
    }

    // Check if it's an object
    if json[valueStart] == '{' {
      // Find matching closing brace (handling nesting)
      var depth = 1;
      var pos = valueStart + 1;
      var inString = false;
      var escaped = false;

      while pos < json.size && depth > 0 {
        const ch = json[pos];
        if escaped {
          escaped = false;
        } else if ch == '\\' && inString {
          escaped = true;
        } else if ch == '"' {
          inString = !inString;
        } else if !inString {
          if ch == '{' then depth += 1;
          else if ch == '}' then depth -= 1;
        }
        pos += 1;
      }

      if depth == 0 {
        return (true, json[valueStart..<pos]);
      }
    }

    return (false, "{}");
  }

  // ============================================================================
  // Request Parsing
  // ============================================================================

  /*
   * Parse a JSON-RPC 2.0 request from a JSON string.
   *
   * Returns (success, request) tuple. If parsing fails, success is false
   * and an empty/default request is returned.
   *
   * Validates:
   * - jsonrpc field is "2.0"
   * - method field is present
   * - id field is present (for requests, not notifications)
   */
  proc parseRequest(line: string): (bool, JsonRpcRequest) {
    var req = new JsonRpcRequest();

    // Strip whitespace
    const json = line.strip();

    // Validate it looks like JSON object
    if json.size < 2 || json[0] != '{' || json[json.size - 1] != '}' {
      stderr.writeln("Protocol: Invalid JSON - not an object");
      return (false, req);
    }

    // Extract jsonrpc version
    const (hasVersion, version) = extractJsonString(json, "jsonrpc");
    if !hasVersion || version != "2.0" {
      stderr.writeln("Protocol: Invalid or missing jsonrpc version");
      return (false, req);
    }
    req.jsonrpc = version;

    // Extract id (may be null for notifications)
    const (hasId, id) = extractJsonInt(json, "id");
    if hasId {
      req.id = id;
    } else {
      // Check if id field exists but is a string
      const (hasStrId, strId) = extractJsonString(json, "id");
      if hasStrId {
        // For now, we only support integer IDs
        stderr.writeln("Protocol: String IDs not supported, using hash");
        req.id = strId.size;  // Simple hash
      } else {
        req.id = -1;  // Notification (no ID)
      }
    }

    // Extract method (required)
    const (hasMethod, method) = extractJsonString(json, "method");
    if !hasMethod || method == "" {
      stderr.writeln("Protocol: Missing or empty method field");
      return (false, req);
    }
    req.method = method;

    // Extract params (optional, defaults to {})
    const (hasParams, params) = extractJsonObject(json, "params");
    if hasParams {
      req.params = params;
    } else {
      req.params = "{}";
    }

    return (true, req);
  }

  // ============================================================================
  // Response Generation
  // ============================================================================

  /*
   * Generate a JSON-RPC 2.0 success response.
   *
   * @param id: Request ID to echo back
   * @param result: JSON-encoded result value
   * @return: Complete JSON response string (no trailing newline)
   */
  proc makeResponse(id: int, result: string): string {
    return '{"jsonrpc":"2.0","id":' + id:string + ',"result":' + result + '}';
  }

  /*
   * Generate a JSON-RPC 2.0 error response.
   *
   * @param id: Request ID to echo back
   * @param code: Error code (negative for standard errors, positive for app errors)
   * @param message: Human-readable error message
   * @return: Complete JSON error response string (no trailing newline)
   */
  proc makeErrorResponse(id: int, code: int, message: string): string {
    const escapedMessage = escapeJsonString(message);
    return '{"jsonrpc":"2.0","id":' + id:string +
           ',"error":{"code":' + code:string +
           ',"message":"' + escapedMessage + '"}}';
  }

  /*
   * Generate a JSON-RPC 2.0 notification (no response expected).
   *
   * Per spec, notifications have no id field.
   *
   * @param method: Notification method name
   * @param params: JSON-encoded parameters
   * @return: Complete JSON notification string (no trailing newline)
   */
  proc makeNotification(methodName: string, params: string): string {
    return '{"jsonrpc":"2.0","method":"' + escapeJsonString(methodName) +
           '","params":' + params + '}';
  }

  // ============================================================================
  // Utility Functions
  // ============================================================================

  /*
   * Check if a request is a notification (has no ID).
   */
  proc isNotification(req: JsonRpcRequest): bool {
    return req.id == -1;
  }

  /*
   * Get a human-readable error message for standard JSON-RPC error codes.
   */
  proc getStandardErrorMessage(code: int): string {
    select code {
      when PARSE_ERROR do return "Parse error";
      when INVALID_REQUEST do return "Invalid request";
      when METHOD_NOT_FOUND do return "Method not found";
      when INVALID_PARAMS do return "Invalid params";
      when INTERNAL_ERROR do return "Internal error";
      otherwise do return "Unknown error";
    }
  }

  /*
   * Get a human-readable error message for application error codes.
   */
  proc getAppErrorMessage(code: int): string {
    select code {
      when IDENTITY_NOT_FOUND do return "Identity not found";
      when SSH_VALIDATION_FAILED do return "SSH validation failed";
      when GIT_OPERATION_FAILED do return "Git operation failed";
      when CONFIG_ERROR do return "Configuration error";
      when KEYCHAIN_ERROR do return "Keychain operation failed";
      when GPG_ERROR do return "GPG operation failed";
      when PROVIDER_CLI_ERROR do return "Provider CLI error";
      otherwise do return "Application error";
    }
  }

  /*
   * Build a simple JSON object from key-value pairs.
   * Values are assumed to already be JSON-encoded.
   */
  proc buildJsonObject(keys: list(string), values: list(string)): string {
    if keys.size != values.size {
      return "{}";
    }

    var result = "{";
    for i in 0..<keys.size {
      if i > 0 then result += ",";
      result += '"' + escapeJsonString(keys[i]) + '":' + values[i];
    }
    result += "}";
    return result;
  }

  /*
   * Build a JSON array from a list of JSON-encoded values.
   */
  proc buildJsonArray(values: list(string)): string {
    var result = "[";
    for i in 0..<values.size {
      if i > 0 then result += ",";
      result += values[i];
    }
    result += "]";
    return result;
  }

  /*
   * Wrap a string value in JSON quotes.
   */
  proc jsonString(s: string): string {
    return '"' + escapeJsonString(s) + '"';
  }

  /*
   * Convert a boolean to JSON.
   */
  proc jsonBool(b: bool): string {
    return if b then "true" else "false";
  }

  /*
   * Convert an integer to JSON.
   */
  proc jsonInt(i: int): string {
    return i: string;
  }
}

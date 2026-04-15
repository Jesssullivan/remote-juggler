{
  description = "RemoteJuggler - Backend-agnostic git identity management with MCP/ACP support";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    # Rust toolchain for GTK GUI
    rust-overlay = {
      url = "github:oxalica/rust-overlay";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # Chapel compiler from Nix-enabled fork (pre-built via Attic binary cache)
    # Provides chapel-system-llvm (LLVM 19), chapel-gnu, chapel-llvm18, etc.
    # Override: nix build --override-input chapel-nix github:jesssullivan/chapel/<branch> .#remote-juggler
    chapel-nix = {
      url = "github:Jesssullivan/chapel/llvm-21-support";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  nixConfig = {
    # Attic binary cache configuration
    extra-substituters = [
      "https://nix-cache.fuzzy-dev.tinyland.dev/tinyland"
      "https://nix-cache.fuzzy-dev.tinyland.dev/main"
    ];
    extra-trusted-public-keys = [
      "tinyland:/o3SR1lItco+g3RLb6vgAivYa6QjmokbnlNSX+s8ric="
      "main:PBDvqG8OP3W2XF4QzuqWwZD/RhLRsE7ONxwM09kqTtw="
    ];
  };

  outputs = { self, nixpkgs, flake-utils, rust-overlay, chapel-nix }:
    {
      # System-independent outputs for consumption by other flakes
      overlays.default = final: prev: {
        remote-juggler = self.packages.${prev.stdenv.hostPlatform.system}.remote-juggler;
        rj-gateway = self.packages.${prev.stdenv.hostPlatform.system}.rj-gateway;
        pinentry-remotejuggler = self.packages.${prev.stdenv.hostPlatform.system}.pinentry-remotejuggler;
      } // (if prev.stdenv.isLinux then {
        remote-juggler-gui = self.packages.${prev.stdenv.hostPlatform.system}.remote-juggler-gui;
      } else {});

      homeManagerModules.default = import ./nix/homeManagerModule.nix;
    }
    //
    flake-utils.lib.eachSystem [
      "x86_64-linux"
      "aarch64-linux"
      "x86_64-darwin"
      "aarch64-darwin"
    ] (system:
      let
        overlays = [ (import rust-overlay) ];
        pkgs = import nixpkgs {
          inherit system overlays;
        };

        # Chapel version configuration
        chapelVersion = "2.7.0";

        # Chapel from Jesssullivan/chapel fork.
        # The fork builds Chapel with system LLVM but its CHPL_HOME layout needs
        # fixup: lib/ is at $out/lib/ but CHPL_HOME points to $out/share/chapel/.
        # This wrapper creates the right symlink structure and bakes in the
        # compiler environment so consumers don't need to set CHPL_* variables.
        chapel-raw = chapel-nix.packages.${system}.chapel-system-llvm;
        llvmPackages = pkgs.llvmPackages_19;
        chapel = pkgs.runCommand "chapel-wrapped" {
          nativeBuildInputs = [ pkgs.makeWrapper ];
        } ''
          mkdir -p $out/bin $out/share/chapel

          # Symlink all CHPL_HOME contents from the fork's build
          for item in ${chapel-raw}/share/chapel/*; do
            ln -sf "$item" "$out/share/chapel/$(basename "$item")"
          done

          # Fix: add lib/ symlink into CHPL_HOME (fork puts it at $out/lib/)
          ln -sf ${chapel-raw}/lib $out/share/chapel/lib

          # Symlink non-chpl binaries directly
          for bin in ${chapel-raw}/bin/*; do
            binname=$(basename "$bin")
            if [ "$binname" != "chpl" ] && [ "$binname" != ".chpl-wrapped" ]; then
              ln -sf "$bin" "$out/bin/$binname"
            fi
          done

          # Wrap chpl with CHPL_HOME, compiler environment, and LLVM paths
          makeWrapper ${chapel-raw}/bin/.chpl-wrapped $out/bin/chpl \
            --set CHPL_HOME "$out/share/chapel" \
            --set-default CHPL_LLVM system \
            --set-default CHPL_LLVM_CONFIG "${llvmPackages.llvm.dev}/bin/llvm-config" \
            --set-default CHPL_HOST_COMPILER llvm \
            --set-default CHPL_HOST_CC "${llvmPackages.clang}/bin/clang" \
            --set-default CHPL_HOST_CXX "${llvmPackages.clang}/bin/clang++" \
            --set-default CHPL_TARGET_CC "${llvmPackages.clang}/bin/clang" \
            --set-default CHPL_TARGET_CXX "${llvmPackages.clang}/bin/clang++" \
            --set-default CHPL_TARGET_CPU none \
            --set-default CHPL_GMP none \
            --set-default CHPL_RE2 bundled \
            --set-default CHPL_UNWIND ${if pkgs.stdenv.isDarwin then "system" else "bundled"} \
            --set-default CHPL_LAUNCHER none \
            --set-default CHPL_COMM none \
            --set-default CHPL_TASKS qthreads \
            --set-default CHPL_TARGET_MEM jemalloc \
            --set-default CHPL_HWLOC bundled \
            --prefix PATH : '${pkgs.lib.makeBinPath [
              pkgs.coreutils pkgs.gnumake pkgs.pkg-config pkgs.python3 pkgs.which
              llvmPackages.clang llvmPackages.llvm
            ]}' \
            --add-flags '-L ${pkgs.xz.out}/lib' \
            ${pkgs.lib.optionalString pkgs.stdenv.isLinux
              "--add-flags '-I ${llvmPackages.clang}/resource-root/include' --add-flags '-I ${llvmPackages.bintools.libc.dev}/include'"
            } \
            ${pkgs.lib.optionalString pkgs.stdenv.isDarwin
              "--add-flags '-I ${llvmPackages.clang}/resource-root/include'"
            }
        '';

        # Rust toolchain for GTK GUI
        # Using Rust 1.85+ for edition2024 support (required by cfg-expr crate)
        rustToolchain = pkgs.rust-bin.stable."1.85.0".default.override {
          extensions = [ "rust-src" "rust-analyzer" ];
          targets = [ ];
        };

        # Custom rustPlatform using our Rust 1.85 toolchain
        rustPlatform = pkgs.makeRustPlatform {
          cargo = rustToolchain;
          rustc = rustToolchain;
        };

        # GTK4 dependencies (Linux only)
        gtkDeps = pkgs.lib.optionals pkgs.stdenv.isLinux (with pkgs; [
          gtk4
          libadwaita
          glib
          cairo
          pango
          gdk-pixbuf
          graphene
        ]);

        # TPM2-TSS dependencies for HSM library (Linux only)
        tpm2Deps = pkgs.lib.optionals pkgs.stdenv.isLinux [
          pkgs.tpm2-tss
        ];

        # TPM testing tools (devShell only, Linux only)
        tpmTestDeps = pkgs.lib.optionals pkgs.stdenv.isLinux [
          pkgs.swtpm
          pkgs.tpm2-tools
        ];

        # Common build inputs for Chapel projects
        chapelBuildInputs = [
          chapel
          pkgs.gnumake
          pkgs.git
        ];

        # Build the Chapel CLI binary using Chapel built from source
        remote-juggler = pkgs.stdenv.mkDerivation {
          pname = "remote-juggler";
          version = "2.1.0";

          src = pkgs.lib.cleanSourceWith {
            src = ./.;
            filter = path: type:
              let baseName = baseNameOf (toString path);
              in !(baseName == "target" ||
                   baseName == ".mason" ||
                   baseName == "gtk-gui" ||
                   baseName == "tray" ||
                   baseName == "docs" ||
                   baseName == "site" ||
                   baseName == ".git");
          };

          nativeBuildInputs = chapelBuildInputs ++ [
            pkgs.which
            pkgs.python3  # Required by chpl's printchplenv wrapper
          ];

          buildPhase = ''
            export HOME=$(mktemp -d)
            mkdir -p target/release

            # HSM header is needed for Chapel's extern declarations even when
            # HSM_NATIVE_AVAILABLE=false (module is defined but not used at runtime).
            ${chapel}/bin/chpl \
              -M src/remote_juggler \
              --ccflags="-I$src/pinentry" \
              ${pkgs.lib.optionalString pkgs.stdenv.isDarwin
                ''--ldflags="-framework Security -framework CoreFoundation"''
              } \
              -sHSM_NATIVE_AVAILABLE=false \
              --fast \
              -o target/release/remote_juggler \
              src/remote_juggler.chpl
          '';

          installPhase = ''
            mkdir -p $out/bin
            cp target/release/remote_juggler $out/bin/remote-juggler
            chmod +x $out/bin/remote-juggler
          '';

          meta = with pkgs.lib; {
            description = "Backend-agnostic git identity management with MCP/ACP support";
            homepage = "https://gitlab.com/tinyland/projects/remote-juggler";
            license = licenses.zlib;
            maintainers = [ ];
            platforms = platforms.unix;
          };
        };

        # Build the pinentry HSM helper library (Linux/macOS)
        pinentry-remotejuggler = pkgs.stdenv.mkDerivation {
          pname = "pinentry-remotejuggler";
          version = "1.0.0";

          src = pkgs.lib.cleanSourceWith {
            src = ./pinentry;
            filter = path: type:
              let baseName = baseNameOf (toString path);
              in !(baseName == "test_hsm" ||
                   baseName == "*.o" ||
                   baseName == "*.dylib" ||
                   baseName == "*.so");
          };

          nativeBuildInputs = [
            pkgs.gnumake
            pkgs.pkg-config
          ];

          buildInputs = pkgs.lib.optionals pkgs.stdenv.isLinux [
            pkgs.tpm2-tss
          ];

          buildPhase = ''
            make all
          '';

          installPhase = ''
            mkdir -p $out/lib $out/bin $out/include

            # Install shared library
            if [ -f libhsm_remotejuggler.so ]; then
              cp libhsm_remotejuggler.so $out/lib/
            elif [ -f libhsm_remotejuggler.dylib ]; then
              cp libhsm_remotejuggler.dylib $out/lib/
            fi

            # Install pinentry script
            cp pinentry-remotejuggler.py $out/bin/pinentry-remotejuggler
            chmod +x $out/bin/pinentry-remotejuggler

            # Install header
            cp hsm.h $out/include/
          '';

          meta = with pkgs.lib; {
            description = "HSM-backed pinentry for RemoteJuggler with TPM/SecureEnclave support";
            homepage = "https://gitlab.com/tinyland/projects/remote-juggler";
            license = licenses.zlib;
            platforms = platforms.unix;
          };
        };

        # Build the Rust GTK GUI (Linux only)
        remote-juggler-gui = pkgs.lib.optionalAttrs pkgs.stdenv.isLinux (
          rustPlatform.buildRustPackage {
            pname = "remote-juggler-gui";
            version = "0.1.0";

            src = ./gtk-gui;

            cargoLock = {
              lockFile = ./gtk-gui/Cargo.lock;
            };

            nativeBuildInputs = [
              pkgs.pkg-config
              pkgs.wrapGAppsHook4
            ];

            buildInputs = gtkDeps;

            # Tests require a display
            doCheck = false;

            meta = with pkgs.lib; {
              description = "GTK4/Libadwaita GUI for RemoteJuggler identity management";
              homepage = "https://gitlab.com/tinyland/projects/remote-juggler";
              license = licenses.zlib;
              platforms = platforms.linux;
            };
          }
        );

        # Build the Go gateway (all platforms)
        rj-gateway = pkgs.buildGoModule {
          pname = "rj-gateway";
          version = "2.1.0";

          src = pkgs.lib.cleanSourceWith {
            src = ./gateway;
            filter = path: type:
              let baseName = baseNameOf (toString path);
              in !(baseName == "Dockerfile");
          };

          vendorHash = null; # Set after first build, or use goModules

          # Skip vendor hash check during development
          proxyVendor = true;

          ldflags = [ "-s" "-w" ];

          meta = with pkgs.lib; {
            description = "RemoteJuggler MCP gateway with tsnet, Setec, and additive credential resolution";
            homepage = "https://github.com/Jesssullivan/remote-juggler";
            license = licenses.zlib;
            platforms = platforms.unix;
          };
        };

      in {
        packages = {
          inherit remote-juggler rj-gateway pinentry-remotejuggler chapel;
          default = remote-juggler;
        } // pkgs.lib.optionalAttrs pkgs.stdenv.isLinux {
          inherit remote-juggler-gui;
        };

        # Development shell with all tools
        devShells.default = pkgs.mkShell {
          name = "remote-juggler-dev";

          buildInputs = [
            # Rust toolchain
            rustToolchain

            # Chapel (built from source with system LLVM)
            chapel

            # GTK4 development (Linux)
          ] ++ gtkDeps ++ [
            # Common tools
            pkgs.git
            pkgs.just
            pkgs.gnumake  # needed by pinentry/Makefile and Chapel internals
            pkgs.pkg-config

            # Rust tools
            pkgs.cargo-watch
            pkgs.cargo-audit

            # CI/CD tools
            pkgs.jq
            pkgs.curl

            # KeePassXC CLI for credential authority
            pkgs.keepassxc

            # Go toolchain for gateway development
            pkgs.go
          ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
            # Xvfb for headless testing
            pkgs.xvfb-run
          ]
          # TPM2-TSS for HSM library builds
          ++ tpm2Deps
          # TPM testing tools
          ++ tpmTestDeps;

          shellHook = ''
            echo "RemoteJuggler Development Environment"
            echo "======================================"
            echo ""
            echo "Chapel version: ${chapelVersion} (from chapel-nix fork, LLVM 19)"
            echo "  Quick test: chpl --version"
            echo ""
            echo "Available commands:"
            echo "  just build        - Build Chapel CLI (debug)"
            echo "  just release      - Build Chapel CLI (release)"
            echo "  just test         - Run Chapel tests"
            ${pkgs.lib.optionalString pkgs.stdenv.isLinux ''
              echo "  cargo build       - Build GTK GUI (in gtk-gui/)"
              echo "  cargo test        - Run GTK GUI tests"
              echo ""
              echo "TPM/HSM development:"
              echo "  just hsm          - Build HSM library"
              echo "  just hsm-test     - Run HSM unit tests"
              echo "  swtpm --version          - Software TPM available"
            ''}
            echo ""
            echo "Attic cache: https://nix-cache.fuzzy-dev.tinyland.dev/tinyland"
            echo ""

            # RemoteJuggler identity auto-switching
            if command -v remote-juggler >/dev/null 2>&1; then
              if [ -d .git ] || git rev-parse --git-dir >/dev/null 2>&1; then
                DETECTED=$(remote-juggler detect --quiet 2>/dev/null)
                if [ -n "$DETECTED" ]; then
                  echo "RemoteJuggler: Detected identity '$DETECTED'"
                fi
              fi
            fi
          '';

          # Ensure pkg-config can find GTK
          PKG_CONFIG_PATH = pkgs.lib.optionalString pkgs.stdenv.isLinux
            (pkgs.lib.makeSearchPath "lib/pkgconfig" gtkDeps);
        };

        # Chapel-only development shell (all platforms)
        devShells.chapel = pkgs.mkShell {
          name = "remote-juggler-chapel";
          buildInputs = chapelBuildInputs;

          shellHook = ''
            echo "Chapel Development Shell"
            echo "========================"
            echo "Chapel ${chapelVersion} (from chapel-nix fork, LLVM 19)"
            echo "  chpl --version"
          '';
        };

        # TPM development shell (Linux only) for HSM testing
        devShells.tpm = pkgs.lib.optionalAttrs pkgs.stdenv.isLinux (pkgs.mkShell {
          name = "remote-juggler-tpm-dev";

          buildInputs = [
            pkgs.tpm2-tss
            pkgs.swtpm
            pkgs.tpm2-tools
            pkgs.gnumake
            pkgs.gcc
            pkgs.pkg-config
          ];

          shellHook = ''
            echo "TPM Development Shell"
            echo "====================="
            echo ""
            echo "TPM2-TSS: $(pkg-config --modversion tss2-esys 2>/dev/null || echo 'not found')"
            echo "swtpm: $(swtpm --version 2>&1 | head -1)"
            echo ""
            echo "Quick Start - Software TPM:"
            echo "  mkdir -p /tmp/swtpm-test"
            echo "  swtpm socket --tpmstate dir=/tmp/swtpm-test --tpm2 \\"
            echo "    --ctrl type=tcp,port=2322 --server type=tcp,port=2321 &"
            echo "  export TPM2TOOLS_TCTI='swtpm:host=localhost,port=2321'"
            echo "  tpm2_getcap properties-fixed"
            echo ""
            echo "Build pinentry with TPM support:"
            echo "  just hsm && just hsm-test"
          '';
        });

        # For `nix flake check`
        checks = {
          # Test that Chapel from-source build works
          chapel-version = pkgs.runCommand "check-chapel-version" {
            nativeBuildInputs = [ chapel ];
          } ''
            ${chapel}/bin/chpl --version > $out
          '';
        };
      }
    );
}

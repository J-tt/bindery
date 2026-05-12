{
  description = "Bindery — automated book download manager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    # nixos-unstable carries Go 1.26+ which satisfies the go.mod 1.25.10 floor.
    nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixos-unstable";
    gomod2nix.url = "github:nix-community/gomod2nix";
    gomod2nix.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = {
    self,
    nixpkgs,
    nixpkgs-unstable,
    gomod2nix,
  }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs {inherit system;};
    pkgs-unstable = import nixpkgs-unstable {inherit system;};
    gomod2nixPkgs = gomod2nix.legacyPackages.${system};

    version = "1.8.0";

    # ---------------------------------------------------------------------------
    # Frontend — built with buildNpmPackage (deterministic sandbox build).
    # nix/ contains node2nix-generated expressions for `nix develop`.
    # ---------------------------------------------------------------------------
    bindery-web = pkgs.buildNpmPackage {
      pname = "bindery-web";
      inherit version;
      src = ./web;
      nodejs = pkgs.nodejs_22;
      # Regenerate with: nix run nixpkgs#prefetch-npm-deps -- web/package-lock.json
      npmDepsHash = "sha256-wYjuWyGwDAumDaKYfC2MlnXCLY+Bb3YA8bU+1c++gQQ=";
      npmBuildScript = "build";
      installPhase = ''
        runHook preInstall
        cp -r dist $out
        runHook postInstall
      '';
    };

    # ---------------------------------------------------------------------------
    # Backend — built with gomod2nix (gomod2nix.toml tracks go.sum hashes).
    # CGO_ENABLED=0 for a fully static binary (matches Dockerfile).
    # ---------------------------------------------------------------------------
    bindery = gomod2nixPkgs.buildGoApplication {
      pname = "bindery";
      inherit version;
      src = ./.;
      modules = ./gomod2nix.toml;
      subPackages = ["cmd/bindery"];
      CGO_ENABLED = "0";
      # Use Go from nixos-unstable (1.26+) to satisfy go.mod's 1.25.10 floor.
      go = pkgs-unstable.go;
      ldflags = ["-w" "-s"];
      preBuild = ''
        mkdir -p internal/webui/dist
        cp -r ${bindery-web}/* internal/webui/dist/
      '';
      meta = {
        description = "Automated book download manager for Usenet & torrents";
        homepage = "https://github.com/vavallee/bindery";
        mainProgram = "bindery";
      };
    };

    # ---------------------------------------------------------------------------
    # Shared Go test base — reuses the gomod2nix vendor env from bindery.
    # ---------------------------------------------------------------------------
    goTestBase = bindery.overrideAttrs (_: {
      doInstall = false;
      installPhase = "touch $out";
      # Don't re-copy the web dist; these checks don't need it.
      preBuild = "";
    });
  in {
    packages.${system} = {
      inherit bindery;
      default = bindery;
    };

    checks.${system} = {
      # Security tests: SSRF, CRLF injection, cookie flags, SQLi, upload abuse.
      # Pure Go — no server required.
      security = goTestBase.overrideAttrs (_: {
        pname = "bindery-check-security";
        checkPhase = ''
          go test -v -count=1 ./tests/security/...
        '';
      });

      # Frontend unit tests via vitest (jsdom, no browser required).
      # Uses buildNpmPackage so the lockfile v3 deps resolve correctly.
      vitest = pkgs.buildNpmPackage {
        pname = "bindery-check-vitest";
        inherit version;
        src = ./web;
        nodejs = pkgs.nodejs_22;
        npmDepsHash = "sha256-wYjuWyGwDAumDaKYfC2MlnXCLY+Bb3YA8bU+1c++gQQ=";
        npmBuildScript = "test";
        installPhase = "touch $out";
      };
    };

    # -------------------------------------------------------------------------
    # NixOS module — import this flake as an input and add the module.
    # -------------------------------------------------------------------------
    nixosModules.default = {
      config,
      lib,
      pkgs,
      ...
    }: let
      cfg = config.services.bindery;
    in {
      options.services.bindery = {
        enable = lib.mkEnableOption "Bindery book management server";

        package = lib.mkOption {
          type = lib.types.package;
          default = self.packages.${pkgs.stdenv.system}.default;
          description = "The bindery package to use.";
        };

        port = lib.mkOption {
          type = lib.types.port;
          default = 8787;
          description = "HTTP port to listen on.";
        };

        dataDir = lib.mkOption {
          type = lib.types.str;
          default = "/var/lib/bindery";
          description = "State directory (database, backups).";
        };

        libraryDir = lib.mkOption {
          type = lib.types.str;
          default = "/var/lib/bindery/books";
          description = "Final book library destination.";
        };

        audiobookDir = lib.mkOption {
          type = lib.types.str;
          default = "";
          description = "Audiobook library directory (falls back to libraryDir if empty).";
        };

        downloadDir = lib.mkOption {
          type = lib.types.str;
          default = "/var/lib/bindery/downloads";
          description = "Download staging directory.";
        };

        audiobookDownloadDir = lib.mkOption {
          type = lib.types.str;
          default = "";
          description = "Audiobook download directory (falls back to downloadDir if empty).";
        };

        logLevel = lib.mkOption {
          type = lib.types.enum ["debug" "info" "warn" "error"];
          default = "info";
          description = "Log verbosity.";
        };

        extraGroups = lib.mkOption {
          type = lib.types.listOf lib.types.str;
          default = [];
          description = "Extra groups for the bindery system user (e.g. [\"media\"]).";
        };

        environmentFile = lib.mkOption {
          type = lib.types.nullOr lib.types.path;
          default = null;
          description = ''
            Path to an environment file (e.g. from agenix/sops) containing
            secrets such as BINDERY_API_KEY.  Passed as EnvironmentFile= to
            the systemd unit.
          '';
        };

        extraEnvironment = lib.mkOption {
          type = lib.types.attrsOf lib.types.str;
          default = {};
          description = "Additional environment variables passed to the service.";
        };
      };

      config = lib.mkIf cfg.enable {
        users.users.bindery = {
          isSystemUser = true;
          group = "bindery";
          extraGroups = cfg.extraGroups;
          home = cfg.dataDir;
          createHome = false;
          description = "Bindery service user";
        };
        users.groups.bindery = {};

        systemd.tmpfiles.rules = [
          "d ${cfg.dataDir} 0750 bindery bindery - -"
        ];

        systemd.services.bindery = {
          description = "Bindery book management server";
          after = ["network-online.target"];
          wants = ["network-online.target"];
          wantedBy = ["multi-user.target"];

          environment =
            {
              BINDERY_PORT = toString cfg.port;
              BINDERY_DB_PATH = "${cfg.dataDir}/bindery.db";
              BINDERY_DATA_DIR = cfg.dataDir;
              BINDERY_LIBRARY_DIR = cfg.libraryDir;
              BINDERY_DOWNLOAD_DIR = cfg.downloadDir;
              BINDERY_LOG_LEVEL = cfg.logLevel;
            }
            // lib.optionalAttrs (cfg.audiobookDir != "") {
              BINDERY_AUDIOBOOK_DIR = cfg.audiobookDir;
            }
            // lib.optionalAttrs (cfg.audiobookDownloadDir != "") {
              BINDERY_AUDIOBOOK_DOWNLOAD_DIR = cfg.audiobookDownloadDir;
            }
            // cfg.extraEnvironment;

          serviceConfig = {
            Type = "simple";
            ExecStart = "${cfg.package}/bin/bindery";
            Restart = "on-failure";
            RestartSec = "5s";
            User = "bindery";
            Group = "bindery";
            StateDirectory = "bindery";
            UMask = "0002";

            # Hardening
            NoNewPrivileges = true;
            PrivateTmp = true;
            ProtectSystem = "strict";
            ProtectHome = true;
            ProtectKernelTunables = true;
            ProtectKernelModules = true;
            ProtectControlGroups = true;
            RestrictNamespaces = true;
            LockPersonality = true;
            MemoryDenyWriteExecute = true;
            RestrictRealtime = true;
            SystemCallFilter = ["@system-service" "~@privileged"];
            ReadWritePaths =
              [
                cfg.dataDir
                cfg.libraryDir
                cfg.downloadDir
              ]
              ++ lib.optional (cfg.audiobookDir != "") cfg.audiobookDir
              ++ lib.optional (cfg.audiobookDownloadDir != "") cfg.audiobookDownloadDir;
          } // lib.optionalAttrs (cfg.environmentFile != null) {
            EnvironmentFile = cfg.environmentFile;
          };
        };
      };
    };

    # -------------------------------------------------------------------------
    # Dev shell
    # -------------------------------------------------------------------------
    devShells.${system}.default = pkgs.mkShell {
      packages = [
        # Go toolchain (unstable, matches go.mod floor) + gomod2nix updater
        pkgs-unstable.go
        gomod2nix.packages.${system}.default

        # Node / frontend
        pkgs.nodejs_22

        # Misc dev tools
        # golangci-lint is a Go tool — run via `go tool golangci-lint run`
        pkgs.govulncheck
        pkgs.biome
      ];

      shellHook = ''
        # Point @biomejs/biome npm package at the Nix-built binary (avoids
        # dynamically-linked binary issues on NixOS).
        export BIOME_BINARY="${pkgs.biome}/bin/biome"

        # Install node_modules if missing or lockfile is newer than node_modules.
        if [[ ! -d web/node_modules || web/package-lock.json -nt web/node_modules ]]; then
          echo "Running npm ci in web/..."
          (cd web && ${pkgs.nodejs_22}/bin/npm ci)
        fi

        echo "bindery dev shell"
        echo "  make dev       — run backend"
        echo "  make web-dev   — run frontend (from web/)"
        echo "  gomod2nix generate — refresh gomod2nix.toml after go.sum changes"
      '';
    };

    # Helper app: refresh gomod2nix.toml
    apps.${system}.update-deps = {
      type = "app";
      program = "${pkgs.writeShellScript "update-deps" ''
        set -e
        cd "$(git rev-parse --show-toplevel)"
        echo "Regenerating gomod2nix.toml..."
        ${gomod2nix.packages.${system}.default}/bin/gomod2nix generate
        echo "Done. Commit gomod2nix.toml alongside go.sum changes."
      ''}";
    };
  };
}

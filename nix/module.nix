# Chat NixOS module
# https://wiki.nixos.org/wiki/NixOS_modules
flake:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.chat;
in
{
  options.services.chat = {
    enable = lib.mkEnableOption "Chat matchmaking server";

    package = lib.mkOption {
      type = lib.types.package;
      default = flake.packages.${pkgs.stdenv.hostPlatform.system}.chat;
      defaultText = lib.literalExpression "flake.packages.\${pkgs.stdenv.hostPlatform.system}.chat";
      description = "The Chat package to use.";
    };

    addr = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8080";
      description = "Listen address for the HTTP server.";
    };

    domain = lib.mkOption {
      type = lib.types.str;
      description = "Canonical domain URL (e.g. https://example.com).";
    };

    debug = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable debug logging.";
    };

    hsts = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable HSTS header.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.chat = {
      description = "Chat Matchmaking Server";
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        Type = "simple";
        ExecStart = lib.concatStringsSep " " (
          [
            (lib.getExe cfg.package)
            "-addr"
            cfg.addr
            "-domain"
            cfg.domain
          ]
          ++ lib.optionals cfg.debug [ "-debug" ]
          ++ lib.optionals cfg.hsts [ "-hsts" ]
        );

        Restart = "on-failure";
        RestartSec = 5;

        # User/group isolation
        DynamicUser = true;

        # Filesystem protection
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        PrivateMounts = true;

        # Kernel protection
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectHostname = true;
        ProtectClock = true;
        ProtectProc = "invisible";

        # Capability/privilege restrictions
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        AmbientCapabilities = "";
        RestrictSUIDSGID = true;
        LockPersonality = true;

        # Namespace/syscall restrictions
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictAddressFamilies = [
          "AF_INET"
          "AF_INET6"
          "AF_UNIX"
        ];
        SystemCallFilter = [
          "@system-service"
          "~@privileged"
        ];
        SystemCallErrorNumber = "EPERM";
        MemoryDenyWriteExecute = true;

        # Syscall architecture restriction
        SystemCallArchitectures = "native";

        # Resource controls
        DevicePolicy = "closed";
        UMask = "0077";
        ProcSubset = "pid";
      };
    };
  };
}

{
  description = "Local-first Google Messages CLI and archive";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = {
    self,
    nixpkgs,
  }: let
    supportedSystems = [
      "aarch64-darwin"
      "aarch64-linux"
      "x86_64-linux"
    ];
    forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
  in {
    packages = forAllSystems (
      system: let
        pkgs = nixpkgs.legacyPackages.${system};
        version = self.shortRev or self.dirtyShortRev or "dev";
        gmcli = pkgs.buildGoModule {
          pname = "gmcli";
          inherit version;
          src = ./.;

          vendorHash = "sha256-4s45VW6Ukm1IMh1TBeqxtixlt8xilda5qatcGJ3Nfqs=";

          ldflags = [
            "-s"
            "-w"
            "-X github.com/fdsouvenir/gmcli/cmd.Version=${version}"
          ];

          # Keep source-default assertions meaningful. buildGoModule otherwise
          # passes the release linker flags to both the binary and go test.
          preCheck = ''
            ldflags=(-buildid=)
          '';

          meta = {
            description = "Local-first Google Messages CLI and archive";
            homepage = "https://github.com/fdsouvenir/gmcli";
            license = pkgs.lib.licenses.agpl3Plus;
            mainProgram = "gmcli";
            platforms = supportedSystems;
          };
        };
      in {
        inherit gmcli;
        default = gmcli;
      }
    );

    apps = forAllSystems (system: {
      gmcli = {
        type = "app";
        program = nixpkgs.lib.getExe self.packages.${system}.gmcli;
        meta.description = "Run gmcli";
      };
      default = self.apps.${system}.gmcli;
    });

    overlays.default = final: _prev: {
      gmcli = self.packages.${final.stdenv.hostPlatform.system}.gmcli;
    };
  };
}

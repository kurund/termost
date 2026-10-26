{
  description = "termost - terminal Mattermost client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in {
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule (finalAttrs: {
          pname = "termost";
          version = "0-unstable-${self.shortRev or self.dirtyShortRev or "dev"}";
          src = self;
          vendorHash = "sha256-c3SCnzjCSr6U9LC7UZRdUmb+FY0gWUywvnd5jI2N8gw=";
          subPackages = [ "cmd/termost" ];
          ldflags = [ "-s" "-w" "-X main.version=${finalAttrs.version}" ];
          meta = {
            description = "Lightweight terminal client for Mattermost";
            homepage = "https://github.com/kurund/termost";
            license = nixpkgs.lib.licenses.agpl3Plus;
            mainProgram = "termost";
            platforms = systems;
          };
        });
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [ go gopls golangci-lint goreleaser ];
          CGO_ENABLED = "0";
        };
        demo = pkgs.mkShell {
          packages = with pkgs; [ go vhs ];
          CGO_ENABLED = "0";
        };
      });
    };
}

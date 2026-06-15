{ pkgs, ... }:

{
  packages = with pkgs; [
    go
    sqlc
  ];

  env.JWT_SECRET = "dev-secret-change-in-production";
  env.DATABASE_PATH = "digesterr.db";
  env.PORT = "8080";

  processes.digesterr.exec = "go run ./cmd/digesterr";
}
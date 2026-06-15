{
  pkgs,
  ...
}: {
  packages = with pkgs; [
    go
    sqlc
    nodejs
    pnpm
  ];

  env.JWT_SECRET = "dev-secret-change-in-production";
  env.DATABASE_PATH = "digesterr.db";
  env.PORT = "8080";

  processes.digesterr.exec = "go run ./cmd/digesterr";
  processes.web.exec = "pnpm --dir web run dev";
}
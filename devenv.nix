{
  pkgs,
  ...
}: {
  packages = with pkgs; [
    go
    sqlc
    nodejs
    pnpm
    entr
  ];

  env.JWT_SECRET = "dev-secret-change-in-production";
  env.DATABASE_PATH = "digesterr.db";
  env.PORT = "8080";
  env.LOGGING_LEVEL = "debug";

  processes.digesterr.exec = "find . -name '*.go' -type f | entr -n -r go run ./cmd/digesterr";
  processes.web.exec = "pnpm --dir web run dev";
}

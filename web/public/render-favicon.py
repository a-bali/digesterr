#!/usr/bin/env python3
"""Render the digesterr favicon assets from SVG using headless Chromium.

ImageMagick's SVG delegate points at rsvg-convert, which is absent here, so it
silently produced blank (fully transparent, single-colour) PNGs. Chromium renders
the SVG correctly and honours transparency, so it is the renderer of record.

Usage: render-favicon.py <out-dir>
"""
import pathlib
import subprocess
import sys
import tempfile

CHROME = "/opt/playwright/chromium-1248/chrome-linux64/chrome"
PUBLIC = pathlib.Path(__file__).resolve().parent


def render(svg: pathlib.Path, size: int, out: pathlib.Path, bg: str | None = None) -> None:
    """Screenshot `svg` at `size`x`size` into `out` as PNG.

    `bg` None means a transparent background: Chromium's default white backdrop
    is suppressed with `--default-background-color=00000000`, otherwise every
    unfilled pixel is opaque white and the icon shows a white box in a browser
    tab that uses a dark theme.
    """
    background = bg or "transparent"
    html = f"""<!doctype html><html><head><style>
      html,body{{margin:0;padding:0;background:{background};}}
      svg{{display:block;width:{size}px;height:{size}px;}}
    </style></head><body>{svg.read_text()}</body></html>"""
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False) as fh:
        fh.write(html)
        page = fh.name
    cmd = [
        CHROME, "--headless=new", "--disable-gpu", "--no-sandbox",
        "--hide-scrollbars", "--force-device-scale-factor=1",
        "--default-background-color=00000000",
        f"--window-size={size},{size}",
        f"--screenshot={out}",
        f"file://{page}",
    ]
    res = subprocess.run(cmd, capture_output=True, text=True, timeout=120)
    if not out.exists():
        sys.exit(f"render failed for {out}:\n{res.stderr[-800:]}")
    pathlib.Path(page).unlink(missing_ok=True)


def main() -> int:
    outdir = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else PUBLIC)
    outdir.mkdir(parents=True, exist_ok=True)
    icon = PUBLIC / "icon.svg"
    touch = PUBLIC / "apple-touch-icon.svg"

    for size in (16, 32, 48, 64, 128, 256):
        render(icon, size, outdir / f"favicon-{size}.png")
        print(f"  favicon-{size}.png")
    # iOS applies its own mask; give it a solid background and no transparency.
    render(touch, 180, outdir / "apple-touch-icon.png", bg="#0B1220")
    print("  apple-touch-icon.png (180)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

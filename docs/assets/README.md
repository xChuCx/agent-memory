# Brand assets

Hand-authored SVGs (no external fonts — system stacks, so they render and
export consistently). Brand color: `#8b5cf6` (violet) on `#0d1117` (dark).

| File | Use |
|---|---|
| `banner.svg` | README header (embedded). 640×200. |
| `social-preview.svg` | Source for the GitHub **Social preview** card. 1280×640. |

## Set the GitHub social preview

GitHub's social preview must be a **raster** image (PNG/JPG, ≥ 640×320 —
use the native 1280×640); it does not accept SVG. Export the SVG to PNG,
then upload it:

```bash
# pick one exporter:
rsvg-convert -w 1280 -h 640 docs/assets/social-preview.svg -o social-preview.png
#   or
inkscape docs/assets/social-preview.svg --export-type=png -w 1280 -h 640 -o social-preview.png
#   or open social-preview.svg in a browser at 1280×640 and screenshot
```

Then: **GitHub → repo → Settings → General → Social preview → Upload an
image** (`social-preview.png`). The PNG is a generated artifact — no need
to commit it (it lives in the repo's GitHub settings).

The export uses the fonts on the machine doing the export; the SVGs fall
back through `Consolas`/`Segoe UI` on Windows and `SF Mono`/system-ui on
macOS, so the result matches the in-repo banner.

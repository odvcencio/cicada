# Studio screenshot evidence

This branch holds annotated screenshots for Cicada Studio pull requests. It is
never merged. Pull request comments link to the images by commit.

- One folder per pull request (`pr-382`, `pr-384`, …).
- Each image is a capture of a running Studio build. The band under the capture
  names the build (branch and commit), the view and the viewport, and explains
  each numbered outline.
- `before` images come from `main` at the commit named in the band; `after`
  images come from the pull request's branch.
- `fixture/night-circuit.cicada` is the song every capture uses.
- `tools/annotate.cjs` is the capture tool. It drives Chrome through
  Playwright against a running `cicada studio`, outlines the elements it is
  given and writes the caption band. Its header comment lists the options.

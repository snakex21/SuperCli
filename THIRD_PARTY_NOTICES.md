# Third-party notices

## ZERO LSP client

Files below `internal/codeintel/lsp` are adapted from the ZERO project:
https://github.com/Gitlawb/zero

MIT License

Copyright (c) 2026 Gitlawb

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Charmbracelet ConPTY

`process_session` uses github.com/charmbracelet/x/conpty on Windows.

MIT License

Copyright (c) 2023 Charmbracelet, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## creack/pty

`process_session` uses github.com/creack/pty on Unix-like systems.

Copyright (c) 2011 Keith Rarick

Permission is hereby granted, free of charge, to any person
obtaining a copy of this software and associated
documentation files (the "Software"), to deal in the
Software without restriction, including without limitation
the rights to use, copy, modify, merge, publish, distribute,
sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall
be included in all copies or substantial portions of the
Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY
KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE
WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR
PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS
OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR
OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

## Chroma code styles

`internal/ui/tui/codestyles/monokai.xml` and `github.xml` are verbatim copies
of the style files shipped with github.com/alecthomas/chroma/v2 v2.27.0. They
are embedded directly so the terminal UI does not initialize chroma's full
style registry at process start.

MIT License

Copyright (C) 2017 Alec Thomas

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Web GUI fonts

The web GUI embeds these fonts (`internal/webgui/assets/fonts/`), all under the SIL Open Font
License 1.1. The full licence texts with their copyright lines are in `LICENSES/fonts/`;
`LICENSES/fonts/MANIFEST.json` lists each file's source URL, version and SHA-256.

Fonts with a Reserved Font Name (IBM Plex, Source Code Pro, Cascadia Code) are shipped
unmodified. Inter, Atkinson Hyperlegible, Noto Sans, JetBrains Mono and Fira Code are
modified: variable fonts instanced to weights 400-700, all subset to Latin, Latin Extended, Cyrillic and
punctuation, and converted to WOFF2 (Fira Code once more without programming ligatures).

- Inter (Version 4.001): `LICENSES/fonts/inter.txt`
- IBM Plex Sans (Version 3.201): `LICENSES/fonts/ibmplexsans.txt`
- Atkinson Hyperlegible (Version 1.006): `LICENSES/fonts/atkinsonhyperlegible.txt`
- Noto Sans (Version 2.015): `LICENSES/fonts/notosans.txt`
- JetBrains Mono (Version 2.211): `LICENSES/fonts/jetbrainsmono.txt`
- Fira Code (Version 5.002): `LICENSES/fonts/firacode.txt`
- Source Code Pro (Version 1.026): `LICENSES/fonts/sourcecodepro.txt`
- IBM Plex Mono (Version 2.3): `LICENSES/fonts/ibmplexmono.txt`
- Cascadia Code (Version 2102.025): `LICENSES/fonts/cascadia.txt`

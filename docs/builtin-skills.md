# Built-in skills

SuperCli ships the `skills-database` catalog as a compressed, lazy-loaded data
pack. The executables contain only its small metadata index; the shared archive
lives at `supercli-data/skills/builtin-skills.zip`. The snapshot contains 1,410
skills and their referenced resources. Files are not extracted at startup:
searching reads metadata only, and applying a skill materializes just that skill
under `supercli-data/cache/builtin-skills/`.

If the archive is absent, SuperCli still starts and every non-skill feature
works normally. Skill search can use the embedded metadata, while applying a
built-in skill reports the exact expected archive path.

Project skills in `skills/` and `.supercli/skills/` override bundled entries
with the same name. Cross-project skills in `supercli-data/skills/` and Claude
Code interoperability skills in `~/.claude/skills/` also remain supported.

The bundled snapshot comes from `skills-database` by snakex21 and is used
under the MIT License:

> Copyright (c) 2026 snakex21
>
> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to
> deal in the Software without restriction, including without limitation the
> rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
> sell copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in
> all copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
> SOFTWARE.

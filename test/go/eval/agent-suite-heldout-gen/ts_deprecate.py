"""TypeScript: rule-based batch annotation of route handlers, with exclusions and decoys."""
from common import Task, d, cmd, ts_filler, dump_json

NODE_TEST = ["node", "--test"]
TAG = "@deprecated Use the repository layer (src/repo) instead."
PKG = {"name": "shopfront", "private": True, "type": "module", "scripts": {"test": "node --test"}}
TSCONFIG = {"compilerOptions": {"target": "ES2022", "module": "NodeNext", "strict": True, "noEmit": True,
                                "allowImportingTsExtensions": True, "verbatimModuleSyntax": True}, "include": ["src", "test"]}

RESOURCES = ["orders", "carts", "customers", "coupons", "inventory", "reviews", "shipping", "payments", "wishlists",
             "returns", "giftCards", "newsletters", "stores", "banners", "reports", "taxRates", "loyalty"]
V2 = ["orders", "customers", "inventory"]
INTERNAL = ["health", "migrations", "cache"]


def cap(s):
    return s[0].upper() + s[1:]


def table(res):
    out = ""
    for ch in res:
        out += "_" + ch.lower() if ch.isupper() else ch
    return out


# Each kind: (template, deprecate?) - template gets name, res, table, Res.
KINDS = {
    "direct": ('''export async function {name}(req: Req): Promise<Res> {{
  const rows = await db.query("SELECT * FROM {table} WHERE owner = $1", [req.user]);
  return ok(rows);
}}''', True),
    "direct-filter": ('''export async function {name}(req: Req): Promise<Res> {{
  if (!req.params.id) {{
    return notFound();
  }}
  const rows = await db.query("SELECT * FROM {table} WHERE id = $1", [req.params.id]);
  return rows.length ? ok(rows[0]) : notFound();
}}''', True),
    "arrow": ('''export const {name} = async (req: Req): Promise<Res> => {{
  await db.query("DELETE FROM {table} WHERE owner = $1 AND archived", [req.user]);
  return ok([]);
}};''', True),
    "repo": ('''export async function {name}(req: Req): Promise<Res> {{
  const row = await repo.find("{table}", req.params.id);
  return row ? ok(row) : notFound();
}}''', False),
    "repo-comment": ('''export async function {name}(req: Req): Promise<Res> {{
  // Before 3.0 this was db.query("SELECT * FROM {table}"); keep it on the repo.
  const rows = await repo.list("{table}", req.user);
  return ok(rows);
}}''', False),
    "query-one": ('''export async function {name}(req: Req): Promise<Res> {{
  const row = await db.queryOne("SELECT count(*) AS n FROM {table} WHERE owner = $1", [req.user]);
  return ok(row);
}}''', False),
    "indirect": ('''async function load{Res}Raw(owner: string): Promise<unknown[]> {{
  return db.query("SELECT * FROM {table} WHERE owner = $1 ORDER BY id", [owner]);
}}

export async function {name}(req: Req): Promise<Res> {{
  return ok(await load{Res}Raw(req.user));
}}''', False),
    "admin": ('''export async function {name}(req: Req): Promise<Res> {{
  await db.query("UPDATE {table} SET archived = true WHERE owner = $1", [req.params.owner ?? req.user]);
  return ok([]);
}}''', False),
    "arrow-repo": ('''export const {name} = async (req: Req): Promise<Res> => ok(await repo.list("{table}", req.user));''', False),
}

NAMES = {
    "direct": "list{Res}", "direct-filter": "get{Res}ById", "arrow": "purgeArchived{Res}", "repo": "find{Res}",
    "repo-comment": "list{Res}ForUser", "query-one": "count{Res}", "indirect": "export{Res}",
    "admin": "archive{Res}Admin", "arrow-repo": "recent{Res}",
}

DOCS = {
    "direct": "Lists the caller's {res}.", "direct-filter": "Returns one of the caller's {res} by id.",
    "arrow": "Removes archived {res} of the caller.", "repo": "Finds one {res} record via the repository.",
    "repo-comment": "Lists {res} owned by the caller.", "query-one": "Counts the caller's {res}.",
    "indirect": "Exports all {res} of the caller.", "admin": "Archives {res} of any owner (admin only).",
    "arrow-repo": "Most recent {res} of the caller.",
}

HEADER = '''import {{ db }} from "{up}db.ts";
import {{ repo }} from "{up}repo/index.ts";
import {{ ok, notFound, type Req, type Res }} from "{up}http.ts";'''


def route_file(rng, res, up, kinds):
    parts = [HEADER.format(up=up)]
    expected = [HEADER.format(up=up)]
    hits = []
    for kind in kinds:
        tmpl, dep = KINDS[kind]
        name = NAMES[kind].format(Res=cap(res))
        body = tmpl.format(name=name, table=table(res), Res=cap(res))
        pre = ""
        if "\n\nexport " in body:  # private helper first, then the exported function
            pre, body = body.split("\n\nexport ", 1)
            pre, body = pre + "\n\n", "export " + body
        has_doc = rng.random() < 0.5
        doc_lines = ["/**", " * " + DOCS[kind].format(res=res)]
        if rng.random() < 0.3:
            doc_lines.append(" * Requires an authenticated caller.")
        if has_doc:
            fx = "\n".join(doc_lines + [" */"]) + "\n" + body
            ex = "\n".join(doc_lines + [" * " + TAG, " */"]) + "\n" + body if dep else fx
        else:
            fx = body
            if dep:
                ex = "/** " + TAG + " */\n" + body
            else:
                ex = body
        parts.append(pre + fx)
        expected.append(pre + ex)
        if dep:
            hits.append(name)
    return "\n\n".join(parts) + "\n", "\n\n".join(expected) + "\n", hits


def build():
    t = Task("ts-deprecate-raw-sql")
    rng = t.rng
    t.fx("package.json", dump_json(PKG))
    t.fx("tsconfig.json", dump_json(TSCONFIG))
    t.fx("src/db.ts", d('''
        // Thin SQL client. Route handlers should go through src/repo instead.
        export const db = {
          rows: [] as unknown[],
          calls: [] as string[],
          async query(sql: string, params: unknown[]): Promise<unknown[]> {
            db.calls.push("query:" + sql + ":" + params.length);
            return db.rows;
          },
          async queryOne(sql: string, params: unknown[]): Promise<unknown> {
            db.calls.push("queryOne:" + sql + ":" + params.length);
            return db.rows[0] ?? null;
          },
        };
        '''))
    t.fx("src/repo/index.ts", d('''
        import { db } from "../db.ts";

        // Repository layer: the only place that should build SQL.
        export const repo = {
          async find(tableName: string, id: string | undefined): Promise<unknown> {
            if (!id) {
              return null;
            }
            return db.queryOne(`SELECT * FROM ${tableName} WHERE id = $1`, [id]);
          },
          async list(tableName: string, owner: string): Promise<unknown[]> {
            return db.query(`SELECT * FROM ${tableName} WHERE owner = $1`, [owner]);
          },
        };
        '''))
    t.fx("src/http.ts", d('''
        export interface Req {
          user: string;
          params: Record<string, string | undefined>;
        }

        export interface Res {
          status: number;
          body: unknown;
        }

        export function ok(body: unknown): Res {
          return { status: 200, body };
        }

        export function notFound(): Res {
          return { status: 404, body: null };
        }
        '''))
    all_kinds = list(KINDS)
    expected_files = {}
    hits_by_file = {}
    route_paths = []
    for i, res in enumerate(RESOURCES):
        k = rng.sample(all_kinds, rng.randint(3, 5))
        if i < 5:  # make sure every kind shows up with and without JSDoc
            k = sorted(set(k) | {all_kinds[(2 * i) % len(all_kinds)], all_kinds[(2 * i + 1) % len(all_kinds)]}, key=all_kinds.index)
        route_paths.append(("src/routes/%s.ts" % res, "../", res, k))
    for res in V2:
        k = rng.sample(all_kinds, rng.randint(3, 4))
        if "direct" not in k:
            k.append("direct")
        route_paths.append(("src/routes/v2/%s.ts" % res, "../../", res, k))
    for res in INTERNAL:
        route_paths.append(("src/routes/internal/%s.ts" % res, "../../", res, ["direct", "arrow", "repo"]))
    total_hits = 0
    for rel, up, res, kinds in route_paths:
        fx, ex, hits = route_file(rng, res, up, kinds)
        t.fx(rel, fx)
        if "/internal/" in rel:
            ex, hits = fx, []
        expected_files[rel] = ex
        hits_by_file[rel] = hits
        total_hits += len(hits)
        if ex != fx:
            t.ref(rel, ex)
    for rel in ["src/jobs/cleanup.ts", "src/jobs/reindex.ts"]:
        name = rel.split("/")[-1][:-3]
        t.fx(rel, d('''
            import { db } from "../db.ts";

            // Nightly job; jobs are migrated separately from the routes.
            export async function run%s(): Promise<number> {
              const rows = await db.query("SELECT id FROM jobs WHERE kind = $1", ["%s"]);
              return rows.length;
            }
            ''' % (cap(name), name)))
    t.fx("test/routes.test.ts", d('''
        import { test } from "node:test";
        import assert from "node:assert/strict";
        import { db } from "../src/db.ts";
        import * as orders from "../src/routes/orders.ts";
        import * as carts from "../src/routes/carts.ts";
        import { runCleanup } from "../src/jobs/cleanup.ts";

        test("route modules load and answer", async () => {
          db.rows = [{ id: "1" }];
          for (const mod of [orders, carts]) {
            for (const [name, fn] of Object.entries(mod)) {
              const res = await (fn as (r: unknown) => Promise<{ status: number }>)({ user: "u1", params: { id: "1" } });
              assert.ok(res.status === 200 || res.status === 404, name);
            }
          }
          assert.equal(await runCleanup(), 1);
        });
        '''))
    t.fx("README.md", d('''
        # shopfront

        HTTP route handlers live in `src/routes/` (one module per resource,
        `src/routes/v2/` for the v2 API, `src/routes/internal/` for operator
        endpoints). SQL should go through the repository layer in `src/repo/`;
        `src/db.ts` is the raw client. Tests: `node --test`.
        '''))
    for sub in ["src/ui", "src/format", "src/i18n"]:
        ts_filler(t, sub, count=4)

    for rel, text in expected_files.items():
        t.gr("graders/expected/" + rel + ".txt", text)
    t.gr("graders/check_deprecations.py", d('''
        import os, sys

        def norm(s):
            return "\\n".join(l.rstrip() for l in s.replace("\\r\\n", "\\n").strip().split("\\n"))

        base = "graders/expected"
        problems = []
        total = 0
        for root, _, names in os.walk(base):
            for n in names:
                exp_path = os.path.join(root, n)
                rel = os.path.relpath(exp_path, base).replace("\\\\", "/")[:-4]
                total += 1
                want = norm(open(exp_path, encoding="utf-8").read())
                try:
                    got = norm(open(rel, encoding="utf-8").read())
                except OSError:
                    problems.append(rel + ": missing")
                    continue
                if got != want:
                    wl, gl = want.split("\\n"), got.split("\\n")
                    for i in range(max(len(wl), len(gl))):
                        a = wl[i] if i < len(wl) else None
                        b = gl[i] if i < len(gl) else None
                        if a != b:
                            problems.append("%s: first difference at line %d: want %r got %r" % (rel, i + 1, a, b))
                            break
        for p in problems:
            print(p)
        if problems:
            print("EVAL_SCORE=%.2f" % (1 - len(problems) / float(total)))
        sys.exit(1 if problems else 0)
        '''))
    t.register(
        name="Annotate exported route handlers that call db.query directly (%d of them, with exclusions and decoys)" % total_hits,
        category="multi-file", tags=["typescript", "batch-edit", "multi-file", "rules"], difficulty="hard",
        prompt=("We are deprecating raw SQL in HTTP route handlers. In every TypeScript file under src/routes/ "
                "(subdirectories included) EXCEPT files under src/routes/internal/, find each exported function - "
                "`export function`, `export async function` and `export const name = ... =>` arrow functions - "
                "whose own body calls the method `db.query(` directly. Calls to other db methods such as "
                "`db.queryOne`, calls through `repo`, calls made only inside a non-exported helper, and mentions in "
                "comments do not count. Functions whose name ends with `Admin` are exempt.\n"
                "Mark each matching function as deprecated with exactly this JSDoc tag text:\n"
                "`" + TAG + "`\n"
                "- If the function already has a JSDoc block (`/** ... */` directly above it; they are all "
                "multi-line), add the line ` * " + TAG + "` as the last line inside that block, just before ` */`.\n"
                "- Otherwise add the single line `/** " + TAG + " */` directly above the `export` line.\n"
                "Change nothing else (no other files, no reformatting). `node --test` must still pass."),
        strictDiff=True,
        allowedChangedFiles=["src/routes/**"],
        forbiddenChangedFiles=["src/routes/internal/**", "src/jobs/**", "src/db.ts", "src/repo/**"],
        expectedChangedFiles=sorted(r for r, h in hits_by_file.items() if h),
        verificationCommands=[cmd("node-test", *NODE_TEST), cmd("deprecations", "python", "graders/check_deprecations.py")],
        fileChecks=[{"id": "jobs-untouched", "path": "src/jobs/*.ts", "notContains": ["@deprecated"]},
                    {"id": "internal-untouched", "path": "src/routes/internal/*.ts", "notContains": ["@deprecated"], "critical": True}],
    )

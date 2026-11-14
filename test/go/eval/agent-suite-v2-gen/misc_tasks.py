"""Language-neutral tasks: parallel read audit and a big-log investigation."""
import json

from common import Task, d, cmd, dump_json


# ------------------------------------------------------------ parallel reads
def parallel_audit():
    t = Task("parallel-service-audit")
    rng = t.rng
    owners_known = ["team-core", "team-pay", "team-growth", "team-data", "team-infra", "team-search"]
    names = sorted("%s-%s" % (a, b) for a in ["ads", "auth", "billing", "cart", "catalog", "email", "feed", "geo", "inbox", "kyc", "ledger", "media", "orders", "pricing", "quota", "ranker", "search", "tax"] for b in ["api", "worker"])
    services = {}
    port = 7000
    for n in names:
        port += rng.choice([1, 2, 3])
        later = [x for x in names if x > n]
        services[n] = {"port": port, "owner": rng.choice(owners_known), "deps": sorted(rng.sample(later, min(len(later), rng.randint(0, 3))))}
    # deliberate problems
    conflicts = [(names[3], names[20]), (names[8], names[30]), (names[11], names[12]), (names[25], names[33])]
    for a, b in conflicts:
        services[b]["port"] = services[a]["port"]
    services[names[5]]["owner"] = "team-payments"      # unknown owner (close to team-pay)
    services[names[17]]["owner"] = "Team-Core"          # case differs -> unknown
    services[names[29]]["owner"] = "team-legacy"
    services[names[7]]["deps"] = sorted(services[names[7]]["deps"] + ["fraud-api"])
    services[names[22]]["deps"] = sorted(set(services[names[22]]["deps"] + ["search-worker", "searchd"]))
    services[names[34]]["deps"] = sorted(services[names[34]]["deps"] + ["ledger-v1"])
    # two dependency cycles (deps otherwise only point "forward" in name order)
    services[names[31]]["deps"] = sorted(set(services[names[31]]["deps"] + [names[2]]))
    services[names[2]]["deps"] = sorted(set(services[names[2]]["deps"] + [names[14]]))
    services[names[14]]["deps"] = sorted(set(services[names[14]]["deps"] + [names[31]]))
    services[names[27]]["deps"] = sorted(set(services[names[27]]["deps"] + [names[19]]))
    services[names[19]]["deps"] = sorted(set(services[names[19]]["deps"] + [names[27]]))
    for n, s in services.items():
        deps = ", ".join('"%s"' % x for x in s["deps"])
        body = d('''
            # %(n)s service manifest
            name = "%(n)s"
            port = %(port)d
            owner = "%(owner)s"
            deps = [%(deps)s]

            [health]
            path = "/healthz"
            interval = "%(iv)ds"

            [resources]
            cpu = "%(cpu)dm"
            memory = "%(mem)dMi"
            ''') % {"n": n, "port": s["port"], "owner": s["owner"], "deps": deps, "iv": rng.choice([5, 10, 15, 30]), "cpu": rng.choice([100, 250, 500]), "mem": rng.choice([128, 256, 512])}
        t.fx("services/%s/service.toml" % n, body)
        t.fx("services/%s/README.md" % n, "# %s\n\nOwned by %s.\n" % (n, s["owner"]))
    t.fx("OWNERS", "".join(o + "\n" for o in owners_known))
    t.fx("README.md", "# platform services\n\nOne directory per service under services/, each with a service.toml manifest.\n")
    # expected
    by_port = {}
    for n, s in services.items():
        by_port.setdefault(s["port"], []).append(n)
    lines = ["# Service audit", "", "## Port conflicts", ""]
    pc = ["%d: %s" % (p, ", ".join(sorted(ns))) for p, ns in sorted(by_port.items()) if len(ns) > 1]
    lines += pc or ["none"]
    lines += ["", "## Unknown owners", ""]
    uo = ["%s: %s" % (n, s["owner"]) for n, s in sorted(services.items()) if s["owner"] not in owners_known]
    lines += uo or ["none"]
    lines += ["", "## Missing dependencies", ""]
    md = ["%s -> %s" % (n, dep) for n, s in sorted(services.items()) for dep in s["deps"] if dep not in services]
    lines += md or ["none"]
    lines += ["", "## Dependency cycles", ""]
    lines += _cycles(services) or ["none"]
    expected = "\n".join(lines) + "\n"
    t.ref("AUDIT.md", expected)
    t.register(
        name="Cross-check 36 service manifests into an exact audit report",
        category="parallel", tags=["parallel-reads", "audit", "toml"], difficulty="medium",
        prompt=("Audit the 36 service manifests in services/*/service.toml against each other and against the OWNERS "
                "file, and write AUDIT.md with exactly this structure (no other text):\n\n"
                "```\n# Service audit\n\n## Port conflicts\n\n<port>: <service>, <service>\n\n## Unknown owners\n\n"
                "<service>: <owner>\n\n## Missing dependencies\n\n<service> -> <dependency>\n\n## Dependency cycles\n\n"
                "<service> -> <service> -> ... -> <first service again>\n```\n\n"
                "- Port conflicts: one line per port used by more than one service, services sorted by name, lines "
                "sorted by port number.\n"
                "- Unknown owners: services whose owner is not listed in OWNERS (exact, case-sensitive match), sorted "
                "by service name.\n"
                "- Missing dependencies: every entry of a service's `deps` that is not the name of a service in this "
                "repository, sorted by service then dependency.\n"
                "- Dependency cycles: every elementary cycle in the dependency graph between services of this "
                "repository, listed once, written starting from its alphabetically smallest service and ending with "
                "that service again; cycle lines sorted alphabetically.\n"
                "- A section with no findings contains the single line `none`.\n"
                "Do not modify any other file."),
        strictDiff=True,
        allowedChangedFiles=["AUDIT.md"],
        expectedChangedFiles=["AUDIT.md"],
        fileChecks=[{"id": "audit-exact", "path": "AUDIT.md", "equals": expected, "critical": True}],
    )


def _cycles(services):
    """All elementary cycles, each rotated to start at its smallest node."""
    found = set()

    def walk(start, node, path):
        for nxt in services[node]["deps"]:
            if nxt not in services:
                continue
            if nxt == start:
                found.add(" -> ".join(path + [start]))
            elif nxt > start and nxt not in path:
                walk(start, nxt, path + [nxt])

    for start in sorted(services):
        walk(start, start, [start])
    return sorted(found)


# ------------------------------------------------------------ big log
def big_log():
    t = Task("big-log-incident")
    rng = t.rng
    endpoints = ["/api/orders", "/api/orders/{id}", "/api/cart", "/api/search", "/api/users/{id}", "/api/checkout", "/healthz"]
    upstreams = ["orders-db", "cart-redis", "search-es", "users-db", "payments-gw"]
    users = ["u%04d" % i for i in range(1, 900)]
    lines = []

    def ts(sec):
        h, rem = divmod(sec, 3600)
        m, s = divmod(rem, 60)
        return "2026-11-02T%02d:%02d:%02dZ" % (h, m, s)

    start = 13 * 3600 + 50 * 60          # 13:50:00
    deploy_start = 14 * 3600 + 1 * 60 + 50
    deploy_done = 14 * 3600 + 2 * 60 + 11
    window_end = 14 * 3600 + 10 * 60
    end = 14 * 3600 + 20 * 60
    rid = 100000
    first_after = None
    users_503 = set()
    fail_by_ep = {}
    sec = start
    while sec <= end:
        for _ in range(rng.randint(3, 6)):
            rid += rng.randint(1, 7)
            ep = rng.choice(endpoints)
            up = {"/api/orders": "orders-db", "/api/orders/{id}": "orders-db", "/api/cart": "cart-redis", "/api/search": "search-es",
                  "/api/users/{id}": "users-db", "/api/checkout": "payments-gw", "/healthz": "-"}[ep]
            user = "-" if rng.random() < 0.15 or ep == "/healthz" else rng.choice(users)
            status = 200
            r = rng.random()
            if deploy_done <= sec <= window_end and ep == "/api/checkout" and r < 0.35:
                status = 503
            elif deploy_done <= sec <= window_end and ep == "/api/cart" and r < 0.08:
                status = rng.choice([502, 503])
            elif r < 0.004:
                status = rng.choice([500, 502, 503, 504])
            elif r < 0.05:
                status = rng.choice([201, 204, 304, 400, 404, 429])
            ms = rng.randint(3, 900) if status < 500 else rng.randint(1000, 30000)
            line = "%s %s req=r%d method=%s path=%s status=%d dur_ms=%d user=%s upstream=%s" % (
                ts(sec), "ERROR" if status >= 500 else "INFO", rid, rng.choice(["GET", "POST"]), ep, status, ms, user, up)
            lines.append((sec, line))
            if status >= 500 and sec >= deploy_done:
                if first_after is None:
                    first_after = ("r%d" % rid, up)
                if sec <= window_end:
                    fail_by_ep[ep] = fail_by_ep.get(ep, 0) + 1
            if status == 503 and deploy_done <= sec <= window_end and user != "-":
                users_503.add(user)
        if sec == deploy_start:
            lines.append((sec, "%s INFO deploy started version=4.7.1 by=ci" % ts(sec)))
        if sec == deploy_done:
            lines.append((sec, "%s INFO deploy finished version=4.7.1 duration_s=21" % ts(sec)))
        if sec == start + 300:
            lines.append((sec, "%s INFO deploy finished version=4.7.0-hotfix duration_s=12" % ts(sec)))
        if rng.random() < 0.02:
            lines.append((sec, "%s DEBUG gc pause_ms=%d heap_mb=%d" % (ts(sec), rng.randint(1, 40), rng.randint(200, 900))))
        sec += 1
    # The deploy-finished marker is printed after the requests of that second.
    log = "\n".join(l for _, l in lines) + "\n"
    t.fx("logs/server.log", log)
    t.fx("README.md", d('''
        # incident 2026-11-02

        `logs/server.log` is the full gateway log around the 4.7.1 rollout.
        Format: `<time> <level> req=<id> method=<m> path=<p> status=<code> dur_ms=<n> user=<id or -> upstream=<name or ->`.
        '''))
    top = max(fail_by_ep.items(), key=lambda kv: kv[1])
    second = sorted(fail_by_ep.values())[-2] if len(fail_by_ep) > 1 else 0
    if top[1] == second:
        raise SystemExit("big-log: tie for top failing endpoint, change seed")
    # first 5xx strictly after the marker line: requests in the marker's second precede the marker
    marker_idx = [i for i, (_, l) in enumerate(lines) if "deploy finished version=4.7.1" in l][0]
    first = None
    for _, l in lines[marker_idx + 1:]:
        if " status=5" in l:
            parts = dict(p.split("=", 1) for p in l.split()[2:])
            first = (parts["req"], parts["upstream"])
            break
    users_503 = set()
    fail_by_ep = {}
    for i, (s, l) in enumerate(lines):
        if i <= marker_idx or s > window_end or " req=" not in l:
            continue
        parts = dict(p.split("=", 1) for p in l.split()[2:])
        code = int(parts["status"])
        if code >= 500:
            fail_by_ep[parts["path"]] = fail_by_ep.get(parts["path"], 0) + 1
        if code == 503 and parts["user"] != "-":
            users_503.add(parts["user"])
    top = max(fail_by_ep.items(), key=lambda kv: kv[1])
    durs = []
    for i, (s_, l) in enumerate(lines):
        if i <= marker_idx or s_ > window_end or " req=" not in l:
            continue
        parts = dict(p.split("=", 1) for p in l.split()[2:])
        if parts["path"] == "/api/search" and parts["status"].startswith("2"):
            durs.append(int(parts["dur_ms"]))
    durs.sort()
    import math
    p95 = durs[math.ceil(0.95 * len(durs)) - 1]
    answer = {"first_5xx_after_deploy": {"request_id": first[0], "upstream": first[1]},
              "distinct_users_503": len(users_503), "top_failing_endpoint": top[0], "top_failing_count": top[1], "search_p95_ms": p95}
    t.ref("ANSWER.json", dump_json(answer))
    t.gr("graders/expected.json", dump_json(answer))
    t.gr("graders/check_answer.py", d('''
        import json, sys
        want = json.load(open("graders/expected.json", encoding="utf-8"))
        try:
            got = json.load(open("ANSWER.json", encoding="utf-8-sig"))
        except Exception as e:
            print("ANSWER.json unreadable:", e); sys.exit(1)
        bad = []
        def get(d, *ks):
            for k in ks:
                if not isinstance(d, dict) or k not in d:
                    return None
                d = d[k]
            return d
        for path in (("first_5xx_after_deploy", "request_id"), ("first_5xx_after_deploy", "upstream"), ("distinct_users_503",), ("top_failing_endpoint",), ("top_failing_count",), ("search_p95_ms",)):
            if get(got, *path) != get(want, *path):
                bad.append("%s: got %r" % (".".join(path), get(got, *path)))
        for b in bad:
            print(b)
        if bad:
            print("EVAL_SCORE=%.2f" % (1 - len(bad) / 6.0))
        sys.exit(1 if bad else 0)
        '''))
    n_lines = len(lines)
    t.register(
        name="Answer incident questions from a %d-line gateway log" % n_lines,
        category="tool-output", tags=["big-output", "logs", "analysis"], difficulty="hard",
        prompt=("logs/server.log is the gateway log around the rollout of version 4.7.1 (format in README.md). Answer "
                "the following and write the answers to ANSWER.json with exactly these keys:\n"
                "```\n{\n  \"first_5xx_after_deploy\": {\"request_id\": \"r...\", \"upstream\": \"...\"},\n"
                "  \"distinct_users_503\": 0,\n  \"top_failing_endpoint\": \"/api/...\",\n  \"top_failing_count\": 0,\n  \"search_p95_ms\": 0\n}\n```\n"
                "- first_5xx_after_deploy: the first request with a 5xx status logged after the line saying that "
                "deploy of version 4.7.1 finished.\n"
                "- distinct_users_503: the number of distinct users (ignore `user=-`) that got status 503 after that "
                "line and up to and including 14:10:00.\n"
                "- top_failing_endpoint / top_failing_count: the path with the most 5xx responses in that same window, "
                "and that count.\n"
                "- search_p95_ms: the 95th percentile (nearest-rank method: the value at 1-based rank ceil(0.95*N) "
                "in the ascending list) of dur_ms over all 2xx responses for path /api/search in that same window.\n"
                "Do not modify the log."),
        forbiddenChangedFiles=["logs/**", "README.md"],
        expectedChangedFiles=["ANSWER.json"],
        verificationCommands=[cmd("answer", "python", "graders/check_answer.py")],
    )


def build():
    parallel_audit()
    big_log()

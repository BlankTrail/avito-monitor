# Summarise every soak arm into the tables the measurement document needs.
# Reads the JSONL the harness wrote; computes nothing the harness could have
# lied about.
import json, io, glob, os, datetime, sys

SOAK = os.path.dirname(os.path.abspath(__file__))


def load(path):
    return [json.loads(l) for l in io.open(path, encoding="utf-8") if l.strip()]


def t(r):
    return datetime.datetime.fromisoformat(r["at"].replace("Z", "+00:00"))


def has_trace(rs):
    """Whether this arm was recorded by a harness that had the Trace field.

    direct-1jar ran before it existed. Its zero pushbacks are a blind spot and
    not a measurement: request 57 took 244 s and came back on a different
    session, which is a refusal the record cannot show. Any comparison that
    treats its zero as data is comparing an instrument against its absence.
    """
    return any("trace" in r for r in rs)


def arm_stats(name, rs):
    good = [r for r in rs if r["class"] in ("ok", "empty")]
    push = [r for r in rs if r.get("pushback")]
    span = (t(rs[-1]) - t(rs[0])).total_seconds() + rs[-1]["elapsed_s"]
    sessions = {r["session"] for r in rs}
    labels = {r["label"] for r in rs}
    first_push = push[0]["n"] if push else None
    bad = [r for r in rs if r["class"] not in ("ok", "empty")]
    # Time spent inside remedies, i.e. the cost of being refused.
    remedy_s = sum(r["elapsed_s"] for r in push) + sum(
        r["elapsed_s"] for r in bad if not r.get("pushback"))
    return dict(
        arm=name, fetches=len(rs), clean=len(good), bad=len(bad),
        pushback=len(push), first_push=first_push,
        first_bad=bad[0]["n"] if bad else None,
        sessions=len(sessions), addresses=len(labels),
        span_min=span / 60, clean_per_hour=len(good) / (span / 3600),
        remedy_min=remedy_s / 60, traced=has_trace(rs),
    )


def clean_runs(rs):
    """Lengths of consecutive stretches with no refusal at all."""
    runs, cur = [], 0
    for r in rs:
        if r["class"] in ("ok", "empty") and not r.get("pushback"):
            cur += 1
        else:
            if cur:
                runs.append(cur)
            cur = 0
    if cur:
        runs.append(cur)
    return runs


rows = []
for path in sorted(glob.glob(os.path.join(SOAK, "soak-*.jsonl"))):
    name = os.path.basename(path)[5:-6]
    rs = load(path)
    if not rs:
        continue
    rows.append((name, rs, arm_stats(name, rs)))

hdr = f"{'arm':<20}{'fetch':>6}{'clean':>6}{'bad':>5}{'push':>6}{'1st push':>9}{'ids':>5}{'addr':>5}{'minutes':>9}{'clean/h':>9}{'in remedy':>11}  instrument"
print(hdr)
print("-" * len(hdr))
for name, rs, s in rows:
    print(f"{s['arm']:<20}{s['fetches']:>6}{s['clean']:>6}{s['bad']:>5}"
          f"{s['pushback']:>6}{str(s['first_push'] or '-'):>9}{s['sessions']:>5}"
          f"{s['addresses']:>5}{s['span_min']:>9.1f}{s['clean_per_hour']:>9.0f}"
          f"{s['remedy_min']:>10.1f}m"
          f"  {'traced' if s['traced'] else 'NO TRACE (blind to refusals)'}")

print("\nclean runs (consecutive fetches with no refusal):")
for name, rs, _ in rows:
    runs = clean_runs(rs)
    print(f"  {name:<20} {runs}")

print("\nevery refusal, in order:")
for name, rs, _ in rows:
    for r in rs:
        if r.get("pushback") or r["class"] not in ("ok", "empty"):
            print(f"  {name:<20} n={r['n']:<5} {r['elapsed_s']:>7.0f}s  "
                  f"{' -> '.join(r.get('trace', [])) or r['class']}")

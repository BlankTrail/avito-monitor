# -*- coding: utf-8 -*-
"""Prove that the mobile surface has a parseable state container, and that an empty result
set is distinguishable from a soft block — without a single regular expression.

The scan below is deliberately the same shape the Go side will have:
  1. tokenise the HTML, take <script> elements that have no src;
  2. accept the one whose text begins with the assignment prefix;
  3. read the FIRST double-quoted JS string literal with a two-state escape-aware scan;
  4. json.Unmarshal it twice — a JS string literal holding JSON text.
"""
import sys, json
from html.parser import HTMLParser

PREFIX = 'window.__initialData__ = '
QUOTE = '"'
BACKSLASH = chr(92)


def first_js_string(s):
    i = s.index(QUOTE)
    out = [s[i]]
    esc = False
    for ch in s[i + 1:]:
        out.append(ch)
        if esc:
            esc = False
            continue
        if ch == BACKSLASH:
            esc = True
            continue
        if ch == QUOTE:
            break
    return ''.join(out)


class Grab(HTMLParser):
    def __init__(self):
        HTMLParser.__init__(self)
        self.cur = None
        self.blob = None

    def handle_starttag(self, tag, attrs):
        if tag == 'script':
            self.cur = dict(attrs)

    def handle_data(self, d):
        if self.cur is not None and self.blob is None and 'src' not in self.cur:
            if d.lstrip().startswith(PREFIX):
                self.blob = d

    def handle_endtag(self, tag):
        if tag == 'script':
            self.cur = None


def load(path):
    g = Grab()
    with open(path, encoding='utf-8') as f:
        g.feed(f.read())
    if not g.blob:
        return None
    body = g.blob.lstrip()[len(PREFIX):]
    return json.loads(json.loads(first_js_string(body)))


INTERESTING = ('emptySearch', 'totalCount', 'count', 'mainCount', 'itemsOnPage',
               'totalElements', 'isBot', 'sortTimeStamp', 'urlPath')

for path, label in ((sys.argv[1], '30 RESULTS'), (sys.argv[2], 'EMPTY RESULTS')):
    d = load(path)
    print('=' * 66)
    print(label, '- parsed:', d is not None)
    if not d:
        continue
    print('  top-level keys:', sorted(d.keys()))
    hits = []

    def dig(o, path_=''):
        if isinstance(o, dict):
            for k, v in o.items():
                if k in INTERESTING:
                    hits.append('%s.%s = %s' % (path_, k, str(v)[:70]))
                dig(v, path_ + '.' + k)
        elif isinstance(o, list):
            for i, v in enumerate(o[:2]):
                dig(v, '%s[%d]' % (path_, i))

    dig(d)
    if hits:
        for h in hits[:14]:
            print('   ', h)
    else:
        print('    (none of the interesting keys present)')

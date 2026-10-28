#!/usr/bin/env bash
# Summarise Bot API spec changes as Markdown for the refresh-spec PR body.
#
# Usage: scripts/spec-diff.sh OLD_API_JSON NEW_API_JSON [SERVER_GO]
#
# Methods registered with explicit handlers in SERVER_GO (default
# pkg/mock/server.go) are listed first: their custom responses may need a
# look. Everything else is served by the spec-driven generic dispatcher, so
# the rest of the report is informational only.
set -euo pipefail

old=${1:?old api.json}
new=${2:?new api.json}
server=${3:-pkg/mock/server.go}

handled=$(grep -oE 'g\.(GET|POST)\("/[A-Za-z]+"' "$server" | sed -E 's/.*"\/([A-Za-z]+)"/\1/' | sort -u | jq -R . | jq -s .)

jq -r -n --slurpfile old "$old" --slurpfile new "$new" --argjson handled "$handled" '
  def shape: {returns, subtypes, fields: ((.fields // []) | map({key: .name, value: {types, required}}) | from_entries)};
  def fielddiff($a; $b):
    ([$b.fields | keys[] | select($a.fields[.] == null) | "+" + .]
     + [$a.fields | keys[] | select($b.fields[.] == null) | "-" + .]
     + [$a.fields | keys[] | select($b.fields[.] != null and $a.fields[.] != $b.fields[.]) | "~" + .]
     + (if $a.returns != $b.returns then ["returns"] else [] end)
     + (if $a.subtypes != $b.subtypes then ["subtypes"] else [] end))
    | join(", ");
  def diff($a; $b):
    ($a | map_values(shape)) as $a | ($b | map_values(shape)) as $b |
    {added:   [$b | keys[] | select($a[.] == null)],
     removed: [$a | keys[] | select($b[.] == null)],
     changed: [$a | keys[] | select($b[.] != null and $a[.] != $b[.]) | {name: ., what: fielddiff($a[.]; $b[.])}]};
  def names: map("`" + . + "`") | join(", ");
  def section($title; $items):
    if ($items | length) == 0 then ""
    elif ($items | length) > 20 then "\n<details><summary><b>\($title)</b> (\($items | length))</summary>\n\n\($items | names)\n</details>\n"
    else "\n**\($title)** (\($items | length)): \($items | names)\n" end;
  def changes($title; $items):
    if ($items | length) > 0 then "\n**\($title)** (\($items | length)):\n" + ($items | map("- `\(.name)`: \(.what)") | join("\n")) + "\n" else "" end;

  $old[0] as $o | $new[0] as $n |
  diff($o.methods; $n.methods) as $m | diff($o.types; $n.types) as $t |
  ($m.changed | map(select(.name as $x | $handled | index($x)))) as $mh |
  ($m.changed | map(select(.name as $x | $handled | index($x) | not))) as $mg |
  "### \($o.version) → \($n.version)\n",
  (if ($m.added + $m.removed + $m.changed + $t.added + $t.removed + $t.changed | length) == 0
   then "No method or type changes."
   else
     changes("Methods with custom handlers that changed - check their responses"; $mh)
     + section("New methods - answered by the generic handler, no action needed"; $m.added)
     + section("Removed methods"; $m.removed)
     + changes("Other changed methods - generic handler, informational"; $mg)
     + section("New types"; $t.added)
     + section("Removed types"; $t.removed)
     + section("Changed types"; ($t.changed | map(.name)))
     + "\n<sub>`+` added field, `-` removed field, `~` changed type or required flag.</sub>"
   end)
'

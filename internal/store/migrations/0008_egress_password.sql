-- Every pass through a proxy list recorded its exit with the password in it:
-- «socks5://login:password@host:port». Found on 11 October 2026 in a real
-- installation's passes table. The transport now writes the exit without it
-- (transport.PublicEgress); this removes what was already written, keeping the
-- login, which is what tells one provider session from another.
UPDATE passes
SET egress =
      substr(egress, 1, instr(egress, '://') + 2)
   || substr(substr(egress, instr(egress, '://') + 3), 1,
             instr(substr(egress, instr(egress, '://') + 3), ':') - 1)
   || '@'
   || substr(substr(egress, instr(egress, '://') + 3),
             instr(substr(egress, instr(egress, '://') + 3), '@') + 1)
WHERE instr(egress, '://') > 0
  AND instr(substr(egress, instr(egress, '://') + 3), '@') > 0
  AND instr(substr(egress, instr(egress, '://') + 3), ':') > 0
  AND instr(substr(egress, instr(egress, '://') + 3), ':')
      < instr(substr(egress, instr(egress, '://') + 3), '@');

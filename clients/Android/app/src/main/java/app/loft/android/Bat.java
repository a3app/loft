/*
 * Android client, shared code: reads and writes route lists in the ".bat"
 * format (Keenetic router / loft-routes). SplitStore uses it to import
 * route lists and to export the user's zones so the same file works on
 * Windows, routers and this app.
 */
package app.loft.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Keenetic / loft-routes `.bat` list. Used for split tunneling: deciding
 * which destination address ranges (prefixes) go through the VPN and which
 * stay on the normal network. A "zone" is a named group of such services
 * in SplitStore. Pure string work, no Android APIs, so it is unit-testable.
 */
final class Bat {
    /** One parsed line: a prefix, its label, and whether it is an "ignore" exclusion. */
    static final class Row {
        /** Label shown in the list (from the rem comment, else the prefix itself). */
        String name = "";
        /** The prefix in CIDR form, e.g. 10.0.0.0/8. */
        String cidr = "";
        /** True for "REM ignore" rows: keep this prefix off the tunnel. */
        boolean ignore;
    }

    // The format is a Windows batch file of "route ADD ip MASK mask gw :: rem name"
    // lines, so the same list works on Windows, Keenetic routers and here.
    // "REM ignore cidr" marks a prefix to keep off the tunnel; "REM zone: id"
    // names the zone the file belongs to.
    /** "route ADD ip [MASK mask]": group 1 is the address, group 2 the optional dotted mask. */
    private static final Pattern ADD = Pattern.compile(
            "(?i)route\\s+ADD\\s+(\\S+)(?:\\s+MASK\\s+(\\S+))?");
    /** Trailing " rem label" or " :: rem label" comment; group 1 is the label. */
    private static final Pattern REM = Pattern.compile("(?i)\\s(?:[:]+\\s*)?rem\\s+(.*)$");
    /** "REM ignore cidr" line; group 1 is the prefix. */
    private static final Pattern IGNORE = Pattern.compile("(?i)^\\s*REM\\s+ignore\\s+(\\S+)");
    /** "REM zone: id" header anywhere in the text (multi-line mode); group 1 is the id. */
    private static final Pattern ZONE = Pattern.compile("(?im)^\\s*REM\\s+zone:\\s*(\\S+)");

    /**
     * Parses a route list. Accepts "route ADD" lines (label from the trailing
     * rem), "REM ignore" lines, and bare CIDRs one per line; comments and other
     * lines are skipped. Rows from all zones in the file are returned
     * together, in file order; zoneHint() reads the zone separately.
     */
    static List<Row> parse(String text) {
        List<Row> out = new ArrayList<>();
        if (text == null || text.isEmpty()) {
            return out;
        }
        String[] lines = text.split("\\r?\\n");
        for (String raw : lines) {
            String line = raw.trim();
            if (line.isEmpty()) {
                continue;
            }
            // 1. "REM ignore cidr" exclusions.
            Matcher ign = IGNORE.matcher(line);
            if (ign.find()) {
                Cidr c = Cidr.parse(ign.group(1));
                if (c != null) {
                    Row r = new Row();
                    r.name = "ignore";
                    r.cidr = c.display();
                    r.ignore = true;
                    out.add(r);
                }
                continue;
            }
            // 2. Other REM lines, the zone header and @echo are not routes.
            if (ZONE.matcher(line).find() || line.regionMatches(true, 0, "REM", 0, 3)
                    || line.regionMatches(true, 0, "@echo", 0, 5)) {
                continue;
            }
            // 3. Split off a trailing "rem label" comment.
            String rem = "";
            Matcher remM = REM.matcher(line);
            String head = line;
            if (remM.find()) {
                rem = remM.group(1).trim();
                head = line.substring(0, remM.start()).trim();
            }
            // 4. A "route ADD ip [MASK mask]" line, or else a bare CIDR (the
            //    whole line is tried, so a CIDR with a trailing comment is skipped).
            Matcher m = ADD.matcher(head);
            if (!m.find()) {
                Cidr c = Cidr.parse(line);
                if (c != null) {
                    Row r = new Row();
                    r.name = c.display();
                    r.cidr = c.display();
                    out.add(r);
                }
                continue;
            }
            String ip = m.group(1);
            String mask = m.group(2);
            Cidr c = mask == null || mask.isEmpty() ? Cidr.parse(ip) : Cidr.fromIpMask(ip, mask);
            if (c == null) {
                continue;
            }
            Row r = new Row();
            r.cidr = c.display();
            r.name = rem.isEmpty() ? c.display() : rem;
            out.add(r);
        }
        return out;
    }

    /** The zone id from the first "REM zone:" header, lower-cased; "" when absent. */
    static String zoneHint(String text) {
        if (text == null) {
            return "";
        }
        Matcher m = ZONE.matcher(text);
        return m.find() ? m.group(1).toLowerCase(Locale.US) : "";
    }

    /**
     * Writes one zone as a .bat list that parse() reads back. Only IPv4 goes in
     * route ADD lines because Windows "route ADD ... MASK" is IPv4-only; IPv6
     * prefixes of normal services are left out of the file. "@echo off"
     * keeps Windows from printing every line when the file is run.
     */
    static String emit(SplitStore st, String zoneId) {
        StringBuilder b = new StringBuilder();
        b.append("@echo off\nREM loft-routes 1.0\n");
        appendZone(b, st, zoneId);
        return b.toString();
    }

    /** All zones in one loft-routes file: each block starts with REM zone:. */
    static String emitAll(SplitStore st) {
        StringBuilder b = new StringBuilder();
        b.append("@echo off\nREM loft-routes 1.0\n");
        for (SplitStore.Zone z : st.zones) {
            appendZone(b, st, z.id);
        }
        return b.toString();
    }

    /**
     * Appends one zone block: header, optional repo line, then each service of
     * the zone as "REM ignore" lines or IPv4 "route ADD" lines.
     */
    private static void appendZone(StringBuilder b, SplitStore st, String zoneId) {
        b.append("\nREM zone: ").append(zoneId).append("\n");
        SplitStore.Zone z = st.zone(zoneId);
        if (z != null && z.repo != null && !z.repo.isEmpty()) {
            b.append("REM repo: ").append(z.repo).append('\n');
        }
        for (SplitStore.Svc s : st.services) {
            if (!zoneId.equals(s.zone)) {
                continue;
            }
            if ("ignore".equals(s.src) || s.ignore) {
                for (String cidr : s.cidrs) {
                    b.append("REM ignore ").append(cidr).append('\n');
                }
                continue;
            }
            for (String cidr : s.cidrs) {
                Cidr c = Cidr.parse(cidr);
                if (c == null || c.v6) {
                    continue;
                }
                // Gateway 0.0.0.0 is a placeholder; ":: rem" starts the label comment.
                b.append("route ADD ").append(c.address())
                        .append(" MASK ").append(Cidr.dottedMask(c.bits))
                        .append(" 0.0.0.0 :: rem ").append(s.name).append('\n');
            }
        }
    }
}

package app.loft.android;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Keenetic / loft-routes `.bat` list. */
final class Bat {
    static final class Row {
        String name = "";
        String cidr = "";
        boolean ignore;
    }

    private static final Pattern ADD = Pattern.compile(
            "(?i)route\\s+ADD\\s+(\\S+)(?:\\s+MASK\\s+(\\S+))?");
    private static final Pattern REM = Pattern.compile("(?i)\\s(?:[:]+\\s*)?rem\\s+(.*)$");
    private static final Pattern IGNORE = Pattern.compile("(?i)^\\s*REM\\s+ignore\\s+(\\S+)");
    private static final Pattern ZONE = Pattern.compile("(?im)^\\s*REM\\s+zone:\\s*(\\S+)");

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
            if (ZONE.matcher(line).find() || line.regionMatches(true, 0, "REM", 0, 3)
                    || line.regionMatches(true, 0, "@echo", 0, 5)) {
                continue;
            }
            String rem = "";
            Matcher remM = REM.matcher(line);
            String head = line;
            if (remM.find()) {
                rem = remM.group(1).trim();
                head = line.substring(0, remM.start()).trim();
            }
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

    static String zoneHint(String text) {
        if (text == null) {
            return "";
        }
        Matcher m = ZONE.matcher(text);
        return m.find() ? m.group(1).toLowerCase(Locale.US) : "";
    }

    static String emit(SplitStore st, String zoneId) {
        StringBuilder b = new StringBuilder();
        b.append("@echo off\nREM loft-routes 1.0\nREM zone: ").append(zoneId).append("\n\n");
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
                b.append("route ADD ").append(c.address())
                        .append(" MASK ").append(Cidr.dottedMask(c.bits))
                        .append(" 0.0.0.0 :: rem ").append(s.name).append('\n');
            }
        }
        return b.toString();
    }
}

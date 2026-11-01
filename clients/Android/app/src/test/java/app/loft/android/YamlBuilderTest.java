/*
 * Android client unit tests (JVM, no device) for YamlBuilder, which writes the
 * client YAML passed to the Go engine. Covers front choices (GET, WebSocket,
 * L3, ...), edge round-trips, merging extra fronts, reading profiles back from
 * YAML, and VLESS links.
 */
package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

/** Tests for turning a Profile into engine YAML and loft:// links, and reading it back. */
public final class YamlBuilderTest {
    /** A GET-front profile writes kind loft, type get and a path, keeps mux on, and parses back. */
    @Test
    public void loftGet() {
        Profile p = new Profile();
        p.name = "Home";
        p.kind = "get";
        p.token = "lt_abc";
        p.url = "https://edge.example.com";
        p.path = "/static/js";
        String y = YamlBuilder.from(p);
        assertTrue(y.contains("kind: loft"));
        assertTrue(y.contains("type: get"));
        assertTrue(y.contains("path:"));
        assertFalse(y.contains("mux: false"));
        Profile back = Profile.fromYaml(y);
        assertTrue(back.isLoft());
        assertTrue("get".equals(back.kind));
    }

    /**
     * The L3 (IP packet) front turns mux off (mux = many streams over one connection) and is
     * kept in the shared loft:// link.
     */
    @Test
    public void loftL3DropsMux() {
        Profile p = new Profile();
        p.name = "Lab";
        p.kind = "l3";
        p.token = "lt_abc";
        p.url = "https://edge.example.com";
        String y = YamlBuilder.from(p);
        assertTrue(y.contains("type: l3"));
        assertTrue(y.contains("mux: false"));
        String uri = YamlBuilder.emit(p);
        assertTrue(uri.contains("type=l3"));
        assertTrue(uri.startsWith("loft://"));
    }

    /** Peek.outbound reads a field of the first outbound without a full YAML parser. */
    @Test
    public void peekOutbound() {
        String y = "kind: loft\ntoken: lt_x\noutbounds:\n  - name: n\n    type: h3\n    url: https://e\n";
        assertTrue("h3".equals(Peek.outbound(y, "type")));
        assertTrue("https://e".equals(Peek.outbound(y, "url")));
    }

    /**
     * A CDN edge in front of the node is written to YAML and to the link, and read back without
     * replacing the node URL.
     */
    @Test
    public void loftEdgeRoundtrip() {
        Profile p = new Profile();
        p.name = "Home";
        p.kind = "get";
        p.token = "lt_abc";
        p.url = "https://node.example.com";
        p.path = "/static/js";
        p.edge = "https://cdn.example.com";
        String y = YamlBuilder.from(p);
        assertTrue(y.contains("edge:"));
        assertTrue(y.contains("cdn.example.com"));
        assertTrue(y.contains("node.example.com"));
        String uri = YamlBuilder.emit(p);
        assertTrue(uri.contains("edge="));
        assertTrue(uri.contains("cdn.example.com"));
        Profile back = Profile.fromYaml(y);
        assertTrue("https://cdn.example.com".equals(back.edge));
        assertTrue("https://node.example.com".equals(back.url));
        assertTrue("/static/js".equals(back.path));
    }

    /**
     * Extra fronts listed by the node (ws, h2, quic) merge into one profile and survive the round
     * trip; learned fronts fill missing paths, and the listed-fronts string follows the paths.
     */
    @Test
    public void mergeExtraFronts() {
        String y = "kind: loft\ntoken: lt_x\nfronts: [get, ws, h2, quic]\n"
                + "outbounds:\n"
                + "  - name: n\n    type: get\n    url: https://node.example.com\n    path: /static/v3/build\n"
                + "  - name: n-ws\n    type: ws\n    url: wss://node.example.com/api/v1/sync\n"
                + "  - name: n-h2\n    type: h2\n    url: https://node.example.com\n    path: /api/v1/tunnel\n";
        Profile p = Profile.fromYaml(y);
        assertTrue("get".equals(p.kind));
        assertTrue("https://node.example.com".equals(p.url));
        assertTrue("/static/v3/build".equals(p.path));
        assertTrue("/api/v1/sync".equals(p.wsPath));
        assertTrue("/api/v1/tunnel".equals(p.h2Path));
        assertTrue(p.quic);
        String back = YamlBuilder.from(p);
        assertTrue(back.contains("fronts:"));
        assertTrue(back.contains("ws"));
        String uri = YamlBuilder.emit(p);
        assertTrue(uri.contains("ws="));
        assertTrue(uri.contains("quic"));
        p.wsPath = "";
        assertTrue(p.applyLearnedFronts("ws=/api/v1/sync"));
        assertTrue("/api/v1/sync".equals(p.wsPath));
        p.kind = "get";
        p.h2Path = "";
        p.h3Path = "";
        p.quic = false;
        p.syncListedFronts();
        assertTrue("get,ws".equals(p.listedFronts));
        p.wsPath = "";
        p.syncListedFronts();
        assertTrue(p.listedFronts.isEmpty());
    }

    /** A profile with only a WebSocket outbound stays WebSocket and gains no GET outbound. */
    @Test
    public void wsOnlyStaysWs() {
        String y = "kind: loft\ntoken: lt_x\nselect: auto\noutbounds:\n"
                + "  - name: alice-phone\n    type: ws\n    url: wss://api.iqos.help/api/v1/sync\n";
        Profile p = Profile.fromYaml(y);
        assertTrue("ws".equals(p.kind));
        String back = YamlBuilder.from(p);
        assertTrue(back.contains("type: ws"));
        assertTrue(back.contains("wss://api.iqos.help/api/v1/sync"));
        assertFalse(back.contains("type: get"));
    }

    /** A GET profile given a wss:// URL dials GET over https and adds a separate ws outbound. */
    @Test
    public void getDoesNotDialWss() {
        Profile p = new Profile();
        p.name = "alice-phone";
        p.kind = "get";
        p.token = "lt_x";
        p.url = "wss://api.iqos.help/api/v1/sync";
        p.path = "/static/v3/build";
        String y = YamlBuilder.from(p);
        assertTrue(y.contains("type: get"));
        assertTrue(y.contains("https://api.iqos.help"));
        assertFalse(y.contains("url: wss://"));
        assertTrue(y.contains("type: ws"));
        assertTrue(y.contains("wss://api.iqos.help/api/v1/sync"));
    }

    /**
     * VLESS YAML keeps the uuid as the token and the link's transport and security are reported.
     */
    @Test
    public void fromYamlReadsUuid() {
        String y = "kind: vless\nuuid: 550e8400-e29b-41d4-a716-446655440000\noutbounds:\n"
                + "  - name: z\n    type: vless\n    url: vless://550e8400-e29b-41d4-a716-446655440000@de1.example.com:443?type=xhttp&security=tls\n";
        Profile p = Profile.fromYaml(y);
        assertTrue("vless".equals(p.kind));
        assertTrue("550e8400-e29b-41d4-a716-446655440000".equals(p.token));
        String back = YamlBuilder.from(p);
        assertTrue(back.contains("uuid:"));
        assertTrue(back.contains("token:"));
        assertTrue("XHTTP".equals(p.linkTransport()));
        assertTrue("TLS".equals(p.linkSecurity()));
    }

    /** Transport and security labels are read from VLESS and Trojan link parameters. */
    @Test
    public void vlessCardsFromUrl() {
        Profile p = new Profile();
        p.kind = "vless";
        p.token = "550e8400-e29b-41d4-a716-446655440000";
        p.url = "vless://x@h:443?type=tcp&security=reality&pbk=abc";
        String y = YamlBuilder.from(p);
        assertTrue(y.contains("kind: vless"));
        assertTrue(y.contains("uuid:"));
        assertTrue("TCP".equals(p.linkTransport()));
        assertTrue("REALITY".equals(p.linkSecurity()));
        p.url = "vless://x@h:443?type=xhttp&security=tls";
        assertTrue("XHTTP".equals(p.linkTransport()));
        assertTrue("TLS".equals(p.linkSecurity()));
        p.url = "trojan://p@h:443?security=none";
        p.kind = "trojan";
        assertTrue("TCP".equals(p.linkTransport()));
        assertTrue("none".equals(p.linkSecurity()));
    }

    /**
     * When an edge is set, the CDN outbound is skipped and the node outbound fills url and path.
     */
    @Test
    public void fromYamlSkipsPinnedCdn() {
        String y = "kind: loft\ntoken: lt_x\nedge: https://cdn.example.com\n"
                + "outbounds:\n"
                + "  - name: cdn\n    type: get\n    url: https://cdn.example.com\n    path: /cdn\n"
                + "  - name: node\n    type: get\n    url: https://node.example.com\n    path: /static/js\n";
        Profile p = Profile.fromYaml(y);
        assertTrue("https://cdn.example.com".equals(p.edge));
        assertTrue("https://node.example.com".equals(p.url));
        assertTrue("/static/js".equals(p.path));
    }
}

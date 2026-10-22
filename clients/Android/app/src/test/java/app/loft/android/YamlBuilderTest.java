package app.loft.android;

import org.junit.Test;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

public final class YamlBuilderTest {
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

    @Test
    public void peekOutbound() {
        String y = "kind: loft\ntoken: lt_x\noutbounds:\n  - name: n\n    type: h3\n    url: https://e\n";
        assertTrue("h3".equals(Peek.outbound(y, "type")));
        assertTrue("https://e".equals(Peek.outbound(y, "url")));
    }
}

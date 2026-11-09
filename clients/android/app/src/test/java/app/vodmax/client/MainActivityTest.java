package app.vodmax.client;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class MainActivityTest {
    private static final String S = "https://tv.example.org";

    @Test
    public void tellsServerPagesApart() {
        assertTrue(MainActivity.serverPage(S, S));
        assertTrue(MainActivity.serverPage(S, S + "/"));
        assertTrue(MainActivity.serverPage(S, S + "/tv.html?ch=1"));
        assertTrue(MainActivity.serverPage(S, S + "/cdn-cgi/access/authorized?token=x"));
        assertTrue(MainActivity.serverPage("http://nas:8095/vodmax", "http://nas:8095/vodmax/"));

        assertFalse(MainActivity.serverPage(S, S + "/auth/sso/callback?code=1"));
        assertFalse(MainActivity.serverPage("http://nas:8095/vodmax", "http://nas:8095/vodmax/auth/sso/start"));
        assertFalse(MainActivity.serverPage(S, "https://team.cloudflareaccess.com/cdn-cgi/access/login/tv.example.org"));
        assertFalse(MainActivity.serverPage(S, "https://tv.example.org.evil.example/"));
        assertFalse(MainActivity.serverPage(S, "http://tv.example.org/"));
        assertFalse(MainActivity.serverPage(S, null));
        assertFalse(MainActivity.serverPage(null, S));
        assertFalse(MainActivity.serverPage("http://nas:8095/vodmax", "http://nas:8095/vodmax2/"));
    }

    @Test
    public void followsTheServerToHttps() {
        assertEquals(S, MainActivity.upgrade("http://tv.example.org", S + "/"));
        assertEquals(S, MainActivity.upgrade("http://tv.example.org", S));
        assertEquals("https://nas:8443", MainActivity.upgrade("http://nas:8443", "https://nas:8443/tv.html"));

        assertNull(MainActivity.upgrade(S, S + "/"));
        assertNull(MainActivity.upgrade("http://tv.example.org", "https://team.cloudflareaccess.com/"));
        assertNull(MainActivity.upgrade("http://tv.example.org", "https://tv.example.org.evil.example/"));
        assertNull(MainActivity.upgrade("http://tv.example.org", "http://tv.example.org/"));
        assertNull(MainActivity.upgrade("http://tv.example.org", null));
    }
}

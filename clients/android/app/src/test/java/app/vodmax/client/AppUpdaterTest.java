package app.vodmax.client;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.junit.Test;

public class AppUpdaterTest {
    private static final String BASE = "https://github.com/silirim/VodMax/releases/download/";

    @Test
    public void acceptsReleaseApks() {
        assertTrue(AppUpdater.releaseApk(BASE + "v0.6.6/VodMax-0.6.6.apk"));
        assertTrue(AppUpdater.releaseApk(BASE + "v0.7.0-beta.1/VodMax-0.7.0-beta.1.apk"));
    }

    // Each of these passed the old prefix check; the connection resolves
    // dot segments, so they reached any repository.
    @Test
    public void refusesEverythingElse() {
        String[] bad = {
            null,
            "",
            BASE + "%2e%2e/%2e%2e/%2e%2e/%2e%2e/evil/x/releases/download/v1/a.apk",
            BASE + "../../../../evil/x/releases/download/v1/a.apk",
            BASE + "./v1/a.apk",
            BASE + "../a.apk",
            BASE + "v1/a.apk?x=1",
            BASE + "v1/a.apk#x",
            BASE + "v1/sub/a.apk",
            BASE + "v1/a.zip",
            BASE + "v1\\..\\a.apk",
            "http://github.com/silirim/VodMax/releases/download/v1/a.apk",
            "https://github.com.evil.example/silirim/VodMax/releases/download/v1/a.apk",
            "https://github.com@evil.example/silirim/VodMax/releases/download/v1/a.apk",
            "https://github.com:443/silirim/VodMax/releases/download/v1/a.apk",
            "https://github.com/silirim/VodMax/releases/download/v1/a.apk\n",
        };
        for (String url : bad) assertFalse(String.valueOf(url), AppUpdater.releaseApk(url));
    }
}

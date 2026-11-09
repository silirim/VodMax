package app.vodmax.client;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertNull;
import static org.junit.Assert.assertTrue;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import org.junit.Test;

public class CopyServiceTest {
    private static final String SERVER = "http://server.example:8095";
    private static final long SIZE = 1000;

    @Test
    public void acceptsOnlyCopyLinks() {
        assertEquals("/stream/save/abc.DEF_-~=123", CopyService.savePath("/stream/save/abc.DEF_-~=123"));
        String[] bad = {
            null,
            "",
            "/stream/save/",
            "/stream/save/..",
            "/stream/save/.",
            "/stream/save/a/b",
            "/stream/save/%2e%2e",
            "/stream/save/a?b=1",
            "/stream/save/a#b",
            "/stream/save/a\n",
            "/stream/vod/abc",
            "/api/v1/save/movie/1",
            "//evil.example/stream/save/a",
            "http://evil.example/stream/save/a",
            SERVER + "/stream/save/a",
        };
        for (String url : bad) assertNull(String.valueOf(url), CopyService.savePath(url));
    }

    @Test
    public void cleansNames() {
        String[] same = {
            "Titolo (2020).mkv",
            "Serie (2019)/Serie S01E02 - Titolo.mkv",
            "Registrazioni/Titolo - Canale 2026-11-08 2100.mp4",
            "a/b/c.mkv",
        };
        for (String n : same) assertEquals(n, CopyService.cleanName(n));
        assertEquals("a/b.mkv", CopyService.cleanName("/a/./b.mkv"));
        assertEquals("a/b.mkv", CopyService.cleanName("a/../b.mkv"));
        assertEquals("x/y.mkv", CopyService.cleanName("../../x/y.mkv"));
        assertEquals("C x y.mkv", CopyService.cleanName("C:\\x?y.mkv"));
        assertEquals("a b.mkv", CopyService.cleanName("a\n\tb.mkv"));
        assertEquals("hidden.mkv", CopyService.cleanName(".hidden.mkv."));
        assertNull(CopyService.cleanName(null));
        assertNull(CopyService.cleanName(""));
        assertNull(CopyService.cleanName("../.."));
        assertNull(CopyService.cleanName("a/b/c/d.mkv")); // three folders
    }

    @Test
    public void readsWhatThePagePasses() {
        CopyService.Copy c = CopyService.fromPage(SERVER, "/stream/save/t0k", "Serie (2019)/E1.mkv", "video/x-matroska", SIZE,
            "\"3e8-1\"", "Serie · S1E1");
        assertEquals(SERVER + "/stream/save/t0k", c.url);
        assertEquals("Serie (2019)/E1.mkv", c.name);
        assertEquals(SIZE, c.size);
        assertEquals("\"3e8-1\"", c.etag);
        assertEquals(CopyService.QUEUED, c.state);
        assertFalse(c.id.isEmpty());
        String[][] bad = {
            {"http://evil.example/stream/save/t", "a.mkv", "video/mp4", "e"},
            {"/stream/save/t", "", "video/mp4", "e"},
            {"/stream/save/t", "a.html", "text/html", "e"},
            {"/stream/save/t", "a.mkv", "video/mp4", ""},
            {"/stream/save/t", "a.mkv", "video/mp4", "e\r\nX-Evil: 1"},
        };
        for (String[] b : bad) assertNull(Arrays.toString(b), CopyService.fromPage(SERVER, b[0], b[1], b[2], SIZE, b[3], ""));
        assertNull(CopyService.fromPage(SERVER, "/stream/save/t", "a.mkv", "video/mp4", 0, "e", ""));
    }

    @Test
    public void readsTheServersAnswers() {
        // First request.
        assertEquals(CopyService.WRITE, CopyService.outcome(200, 0, SIZE, SIZE, null));
        assertEquals(CopyService.UNREACHABLE, CopyService.outcome(200, 0, SIZE, 512, null)); // a proxy's page
        assertEquals(CopyService.UNREACHABLE, CopyService.outcome(200, 0, SIZE, -1, null));
        // Resuming from 400.
        assertEquals(CopyService.APPEND, CopyService.outcome(206, 400, SIZE, 600, "bytes 400-999/1000"));
        assertEquals(CopyService.RESTART, CopyService.outcome(206, 400, SIZE, 600, "bytes 0-999/1000"));
        assertEquals(CopyService.RESTART, CopyService.outcome(206, 400, SIZE, 600, null));
        assertEquals(CopyService.WRITE, CopyService.outcome(200, 400, SIZE, SIZE, null)); // If-Range did not match
        assertEquals(CopyService.RESTART, CopyService.outcome(416, 400, SIZE, -1, null));
        assertEquals(CopyService.NETWORK, CopyService.outcome(416, 0, SIZE, -1, null)); // no loop without a Range
        for (int code : new int[] {301, 302, 303, 307, 308, 401, 403}) {
            assertEquals(String.valueOf(code), CopyService.UNREACHABLE, CopyService.outcome(code, 400, SIZE, -1, null));
        }
        assertEquals(CopyService.EXPIRED, CopyService.outcome(404, 400, SIZE, -1, null));
        assertEquals(CopyService.CHANGED, CopyService.outcome(410, 400, SIZE, -1, null));
        for (int code : new int[] {500, 502, 503, 504, 530, 408, 429}) {
            assertEquals(String.valueOf(code), CopyService.NETWORK, CopyService.outcome(code, 400, SIZE, -1, null));
        }
    }

    @Test
    public void waitsBetweenTries() {
        long[] want = {5_000, 10_000, 20_000, 40_000, 80_000, 160_000, 300_000, 300_000};
        for (int i = 0; i < want.length; i++) assertEquals(want[i], CopyService.backoff(i));
        assertEquals(300_000, CopyService.backoff(100)); // no overflow
        assertFalse(CopyService.paused(0, 29 * 60_000L));
        assertTrue(CopyService.paused(0, 30 * 60_000L));
    }

    @Test
    public void knowsTheFat32Limit() {
        assertFalse(CopyService.fat32(0xFFFFFFFFL - 10, 10));
        assertTrue(CopyService.fat32(0xFFFFFFFFL - 10, 11));
        assertTrue(CopyService.fat32(0xFFFFFFFFL, 1));
        // Already past 4 GB: not a FAT32 stick, so a stick pulled out or a
        // full disk, which must not end the copy for good.
        assertFalse(CopyService.fat32(0xFFFFFFFFL + 1, 1));
        assertFalse(CopyService.fat32(5_000_000_000L, 10));
        assertFalse(CopyService.fat32(6_000_000_000L, 256 * 1024));
    }

    @Test
    public void keepsWifiAwakeWhereAndroidAllows() {
        int highPerf = 3; // WifiManager.WIFI_MODE_FULL_HIGH_PERF, deprecated from Android 14
        assertEquals(highPerf, CopyService.wifiLockMode(24));
        assertEquals(highPerf, CopyService.wifiLockMode(33));
        assertEquals(0, CopyService.wifiLockMode(34));
        assertEquals(0, CopyService.wifiLockMode(35));
    }

    @Test
    public void writesAndReadsTheQueue() {
        CopyService.Copy a = CopyService.fromPage(SERVER, "/stream/save/a=b", "Serie (2019)/S01E01 - 100% + uno.mkv",
            "video/x-matroska", 5_000_000_000L, "\"e%1+\"", "Titolo con spazi\ne a capo · è");
        a.folder = "content://com.android.externalstorage.documents/tree/1234-ABCD%3AVodMax";
        a.part = "content://com.android.externalstorage.documents/tree/1234-ABCD%3AVodMax/document/1234-ABCD%3AVodMax%2Fx.part";
        a.state = CopyService.PAUSED;
        a.done = 4_294_967_295L;
        CopyService.Copy b = CopyService.fromPage(SERVER, "/stream/save/b", "Film (2001).mp4", "video/mp4", 7, "x", "");
        b.folder = CopyService.MOVIES;
        List<CopyService.Copy> list = new ArrayList<>(Arrays.asList(a, b));

        String text = CopyService.encode(list);
        assertEquals(2, text.split("\n").length);
        List<CopyService.Copy> back = CopyService.decode(text + "damaged line\n\n");
        assertEquals(2, back.size());
        for (int i = 0; i < 2; i++) {
            CopyService.Copy want = list.get(i), got = back.get(i);
            assertEquals(want.id, got.id);
            assertEquals(want.url, got.url);
            assertEquals(want.name, got.name);
            assertEquals(want.mime, got.mime);
            assertEquals(want.size, got.size);
            assertEquals(want.etag, got.etag);
            assertEquals(want.title, got.title);
            assertEquals(want.folder, got.folder);
            assertEquals(want.part, got.part);
            assertEquals(want.state, got.state);
            assertEquals(want.done, got.done);
        }
        assertTrue(CopyService.decode("").isEmpty());
    }

    @Test
    public void tellsFinishedCopies() {
        for (String s : new String[] {"queued", "copying", "wifi", "network", "folder", "paused"}) assertFalse(s, CopyService.finished(s));
        for (String s : new String[] {"done", "unreachable", "expired", "changed", "fat32", "space", "failed"}) assertTrue(s, CopyService.finished(s));
    }
}

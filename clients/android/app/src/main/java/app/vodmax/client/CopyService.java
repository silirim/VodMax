package app.vodmax.client;

import android.annotation.SuppressLint;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.pm.ServiceInfo;
import android.database.Cursor;
import android.media.MediaScannerConnection;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.ParcelFileDescriptor;
import android.os.PowerManager;
import android.os.StatFs;
import android.os.SystemClock;
import android.os.storage.StorageManager;
import android.os.storage.StorageVolume;
import android.provider.DocumentsContract;
import android.provider.MediaStore;
import android.system.Os;
import android.system.StructStatVfs;
import android.util.AtomicFile;

import java.io.File;
import java.io.FileDescriptor;
import java.io.FileNotFoundException;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.UnsupportedEncodingException;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLDecoder;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.regex.Pattern;

/**
 * Copies a file that is already on the server's disk (a film, an episode,
 * a recording) to this device, to watch it with no network (decision 0056):
 * Movies/VodMax on the phone, a folder chosen with Android's picker (SD
 * card, USB stick, disk) or, on a TV box without the picker, the app's own
 * folder on the stick. Android's DownloadManager cannot write to a chosen
 * folder, gives up on a 502 from cloudflared and follows redirects (it
 * would save a proxy's login page as the film): the copy is done here, one
 * at a time, in a foreground service that goes on with the app closed.
 * The queue lives in a private file; the app restarts it when it opens.
 */
public class CopyService extends Service {
    // In MainActivity's preferences.
    static final String KEY_FOLDER = "copyFolder"; // "", MOVIES, APP or a tree URI
    static final String KEY_MOBILE = "copyMobile";
    static final String MOVIES = "movies", APP = "app";

    // States, as the page reads them in copies(). The waiting ones and
    // "paused" are not finished. The page offers Togli only on DONE, whose
    // file stays, and Annulla on the others, whose piece is deleted.
    static final String QUEUED = "queued", COPYING = "copying", WIFI = "wifi", NETWORK = "network",
        FOLDER = "folder", PAUSED = "paused", DONE = "done";
    static final String UNREACHABLE = "unreachable", EXPIRED = "expired", CHANGED = "changed", FAT32 = "fat32",
        SPACE = "space", FAILED = "failed";
    // What to do with an answer of the server, besides the states above.
    static final String WRITE = "write", APPEND = "append", RESTART = "restart";

    static final long PAUSE_MS = 30 * 60_000L;
    private static final long FAT32_MAX = 0xFFFFFFFFL;
    private static final String CHANNEL = "copies";
    private static final int NOTE = 56;
    // The signed link of the server; the whole path is matched, so that no
    // dot segment or escape reaches another route.
    private static final Pattern SAVE_PATH = Pattern.compile("/stream/save/(?!\\.\\.?$)[A-Za-z0-9._~=-]+");

    /** One copy; the fields in the queue file, in encode's order. */
    static final class Copy {
        String id = "", url = "", name = "", mime = "", etag = "", title = "", folder = "", part = "", state = QUEUED;
        long size, done;
        volatile boolean cancelled;
    }

    private static final Object LOCK = new Object();
    private static List<Copy> copies;
    private static int workers; // under LOCK; one, but a stopped service's may still be ending
    private static volatile Copy current;

    private final Handler main = new Handler(Looper.getMainLooper());
    private Thread worker;
    private int lastStart;
    private volatile boolean stopping;
    private volatile HttpURLConnection connection;
    private long lastData, lastNote;
    private boolean gotData;

    // ---- Checks on what the page sends (JVM tests) ----

    /** The link's path when it is one of the server's copy links, else null. */
    static String savePath(String url) {
        return url != null && SAVE_PATH.matcher(url).matches() ? url : null;
    }

    /**
     * The name as the server makes it (fileName in 0025), cleaned again: no
     * characters FAT32 refuses, no "." or ".." folders, at most two folders.
     * Null when nothing is left or it is too deep.
     */
    static String cleanName(String name) {
        if (name == null) return null;
        StringBuilder out = new StringBuilder();
        int parts = 0;
        for (String p : name.split("/", -1)) {
            StringBuilder b = new StringBuilder();
            for (char ch : p.toCharArray()) b.append(ch < 32 || "<>:\"\\|?*".indexOf(ch) >= 0 ? ' ' : ch);
            String s = b.toString().trim().replaceAll("\\s+", " ").replaceAll("^[. ]+|[. ]+$", "");
            if (s.isEmpty()) continue;
            out.append(parts++ > 0 ? "/" : "").append(s);
        }
        return parts == 0 || parts > 3 ? null : out.toString(); // String.join needs Android 8
    }

    /** A copy from what the page passed to copy(), or null if anything is off. */
    static Copy fromPage(String server, String url, String name, String mime, long size, String etag, String title) {
        String path = savePath(url), clean = cleanName(name);
        // The etag goes into a header: printable ASCII only.
        if (server == null || path == null || clean == null || mime == null || !mime.startsWith("video/") || size <= 0
            || etag == null || !etag.matches("[\\x21-\\x7e]{1,200}")) return null;
        Copy c = new Copy();
        c.id = UUID.randomUUID().toString();
        c.url = server + path;
        c.name = clean;
        c.mime = mime;
        c.size = size;
        c.etag = etag;
        c.title = title == null ? "" : title;
        return c;
    }

    // ---- The server's answers (JVM tests) ----

    /**
     * What an answer means for a copy asking from byte "from" (0: the whole
     * file) of a file of "size" bytes: WRITE from the start, APPEND, RESTART
     * from zero, NETWORK (try again later) or why it stops.
     */
    static String outcome(int code, long from, long size, long length, String range) {
        if (code == 200) return length == size ? WRITE : UNREACHABLE; // not the file: a proxy's page
        if (code == 206) {
            if (range != null && range.trim().equals("bytes " + from + "-" + (size - 1) + "/" + size)) return from == 0 ? WRITE : APPEND;
            return from > 0 ? RESTART : NETWORK;
        }
        if (code == 416) return from > 0 ? RESTART : NETWORK;
        if ((code >= 300 && code < 400) || code == 401 || code == 403) return UNREACHABLE;
        if (code == 404) return EXPIRED;
        if (code == 410) return CHANGED;
        // A restart or an update of the server: cloudflared answers 502, Cloudflare 530.
        if (code >= 500 || code == 408 || code == 429) return NETWORK;
        return UNREACHABLE;
    }

    /** The wait before try number n (from 0): 5 s, doubling up to 5 minutes. */
    static long backoff(int n) {
        return n >= 6 ? 300_000L : Math.min(300_000L, 5_000L << n);
    }

    static boolean paused(long lastData, long now) {
        return now - lastData >= PAUSE_MS;
    }

    /** The Wi-Fi lock a copy holds on this Android, or 0 for none. */
    @SuppressWarnings("deprecation")
    static int wifiLockMode(int sdk) {
        // HIGH_PERF keeps Wi-Fi out of power save with the screen off. From
        // Android 14 it becomes LOW_LATENCY, which holds only with the screen
        // on and the app in front, and may lower the throughput: none there.
        return sdk < 34 ? WifiManager.WIFI_MODE_FULL_HIGH_PERF : 0;
    }

    /**
     * A write failing as it crosses 4 GB: a FAT32 stick. A file past 4 GB is
     * on another file system, so a later failure is a stick pulled out or a
     * full disk, and must not end the copy for good.
     */
    static boolean fat32(long pos, int n) {
        return pos <= FAT32_MAX && pos + n > FAT32_MAX;
    }

    // ---- The queue file (JVM tests): a line per copy, fields escaped ----

    static String encode(List<Copy> list) {
        StringBuilder b = new StringBuilder();
        for (Copy c : list) {
            String[] f = {c.id, c.url, c.name, c.mime, String.valueOf(c.size), c.etag, c.title, c.folder, c.part, c.state,
                String.valueOf(c.done)};
            for (int i = 0; i < f.length; i++) b.append(i > 0 ? " " : "").append(escape(f[i]));
            b.append('\n');
        }
        return b.toString();
    }

    static List<Copy> decode(String text) {
        List<Copy> list = new ArrayList<>();
        for (String line : text.split("\n")) {
            String[] f = line.split(" ", -1);
            if (f.length < 11) continue;
            try {
                Copy c = new Copy();
                c.id = unescape(f[0]);
                c.url = unescape(f[1]);
                c.name = unescape(f[2]);
                c.mime = unescape(f[3]);
                c.size = Long.parseLong(unescape(f[4]));
                c.etag = unescape(f[5]);
                c.title = unescape(f[6]);
                c.folder = unescape(f[7]);
                c.part = unescape(f[8]);
                c.state = unescape(f[9]);
                c.done = Long.parseLong(unescape(f[10]));
                list.add(c);
            } catch (RuntimeException ignored) {
                // a damaged line: that copy is lost, the others stay
            }
        }
        return list;
    }

    private static String escape(String s) {
        try {
            return URLEncoder.encode(s == null ? "" : s, StandardCharsets.UTF_8.name());
        } catch (UnsupportedEncodingException e) {
            throw new IllegalStateException(e);
        }
    }

    private static String unescape(String s) {
        try {
            return URLDecoder.decode(s, StandardCharsets.UTF_8.name());
        } catch (UnsupportedEncodingException e) {
            throw new IllegalStateException(e);
        }
    }

    static boolean finished(String state) {
        return !(QUEUED.equals(state) || COPYING.equals(state) || WIFI.equals(state) || NETWORK.equals(state)
            || FOLDER.equals(state) || PAUSED.equals(state));
    }

    // ---- The queue, for MainActivity ----

    private static AtomicFile file(Context ctx) {
        return new AtomicFile(new File(ctx.getFilesDir(), "copies.txt"));
    }

    // Under LOCK.
    private static List<Copy> list(Context ctx) {
        if (copies == null) {
            try {
                copies = decode(new String(file(ctx).readFully(), StandardCharsets.UTF_8));
            } catch (IOException e) {
                copies = new ArrayList<>();
            }
        }
        return copies;
    }

    // Under LOCK.
    private static void save(Context ctx) {
        AtomicFile f = file(ctx);
        FileOutputStream out = null;
        try {
            out = f.startWrite();
            out.write(encode(copies).getBytes(StandardCharsets.UTF_8));
            f.finishWrite(out);
        } catch (IOException e) {
            if (out != null) f.failWrite(out);
        }
    }

    /** The copies, newest last, as copies at this moment. */
    static List<Copy> snapshot(Context ctx) {
        synchronized (LOCK) {
            return new ArrayList<>(list(ctx));
        }
    }

    /**
     * Queues a copy into c.folder: "" (queued), "copying" (the same file is
     * queued or being copied), "already" (it is in the folder) or "space".
     * A copy of the same file that stopped (link expired, server out of
     * reach) gives way to this one, which goes on from its piece if the
     * file on the server is the same.
     */
    static String add(Context ctx, Copy c) {
        ctx = ctx.getApplicationContext();
        boolean exists = exists(ctx, c.folder, c.name);
        long free = free(ctx, c.folder);
        Copy stale = null;
        synchronized (LOCK) {
            List<Copy> l = list(ctx);
            Copy old = null;
            long queued = 0;
            for (Copy o : l) {
                if (!o.folder.equals(c.folder)) continue;
                if (o.name.equals(c.name)) {
                    if (!finished(o.state)) return "copying";
                    old = o;
                } else if (!finished(o.state)) queued += o.size - o.done;
            }
            if (exists) return "already";
            boolean resume = old != null && !DONE.equals(old.state) && old.etag.equals(c.etag);
            if (free >= 0 && free < queued + c.size - (resume ? old.done : 0)) return "space";
            if (old != null) {
                l.remove(old);
                if (resume) {
                    c.part = old.part;
                    c.done = old.done;
                } else if (!DONE.equals(old.state)) stale = old;
            }
            l.add(c);
            save(ctx);
        }
        if (stale != null) {
            Copy s = stale;
            Context app = ctx;
            new Thread(() -> deletePart(app, s)).start();
        }
        return "";
    }

    /** Annulla (any copy but DONE: its piece is deleted) or Togli (DONE: the file stays). */
    static void cancel(Context ctx, String id) {
        Context app = ctx.getApplicationContext();
        Copy gone = null;
        synchronized (LOCK) {
            for (Copy c : list(app)) {
                if (c.id.equals(id)) {
                    gone = c;
                    break;
                }
            }
            if (gone == null) return;
            list(app).remove(gone);
            save(app);
        }
        if (DONE.equals(gone.state)) return; // the file stays
        gone.cancelled = true;
        if (gone == current) return; // the worker deletes it
        Copy c = gone;
        new Thread(() -> deletePart(app, c)).start();
    }

    /**
     * A folder was chosen again (the stick's code changed, another stick):
     * copies waiting for an unreachable folder start over in the new one.
     * Off the main thread.
     */
    static void moveWaiting(Context ctx, String folder) {
        Context app = ctx.getApplicationContext();
        List<Copy> waiting = new ArrayList<>();
        for (Copy c : snapshot(app)) {
            if (!finished(c.state) && !c.folder.equals(folder) && !reachable(app, c.folder)) waiting.add(c);
        }
        if (waiting.isEmpty()) return;
        synchronized (LOCK) {
            for (Copy c : waiting) {
                c.folder = folder;
                c.part = "";
                c.done = 0;
            }
            save(app);
        }
    }

    /**
     * The app opened, or a copy was asked: copies paused (30 minutes without
     * data, Android 15's 6 hours) or cut off by Android go on, in the order
     * asked. Only from the open app, as Android 12 wants for a foreground
     * service.
     */
    static void resume(Context ctx) {
        ctx = ctx.getApplicationContext();
        boolean any = false;
        synchronized (LOCK) {
            for (Copy c : list(ctx)) {
                if (PAUSED.equals(c.state) || (workers == 0 && !finished(c.state))) c.state = QUEUED;
                any |= QUEUED.equals(c.state);
            }
            if (any) save(ctx);
        }
        if (any) start(ctx);
    }

    static void start(Context ctx) {
        Intent i = new Intent(ctx, CopyService.class);
        try {
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i);
            else ctx.startService(i);
        } catch (RuntimeException e) {
            // Android refuses it (in the background, 6 hours used up): the
            // copies stay queued until the app opens again.
        }
    }

    // ---- Where the files go ----

    /** A USB stick, a disk or an SD card is mounted. */
    static boolean removable(Context ctx) {
        StorageManager sm = ctx.getSystemService(StorageManager.class);
        if (sm == null) return false;
        for (StorageVolume v : sm.getStorageVolumes()) {
            if (v.isRemovable() && Environment.MEDIA_MOUNTED.equals(v.getState())) return true;
        }
        return false;
    }

    /** The app's folder on a stick or SD card (Android/data/app.vodmax.client/files), or null. */
    static File appDir(Context ctx) {
        File[] dirs = ctx.getExternalFilesDirs(null);
        for (int i = 1; i < dirs.length; i++) { // [0] is the device's own storage
            if (dirs[i] != null && Environment.MEDIA_MOUNTED.equals(Environment.getExternalStorageState(dirs[i]))) return dirs[i];
        }
        return null;
    }

    @SuppressWarnings("deprecation")
    private static File moviesDir() {
        return new File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_MOVIES), "VodMax");
    }

    /** The folder as the page shows it. */
    static String folderName(Context ctx, String folder) {
        if (MOVIES.equals(folder)) return "Movies/VodMax";
        if (APP.equals(folder)) return "Android/data/" + ctx.getPackageName() + "/files";
        if (folder.isEmpty()) return "";
        Uri tree = Uri.parse(folder);
        try (Cursor c = ctx.getContentResolver().query(root(tree), new String[] {DocumentsContract.Document.COLUMN_DISPLAY_NAME},
            null, null, null)) {
            if (c != null && c.moveToFirst() && c.getString(0) != null) return c.getString(0);
        } catch (Exception ignored) {
        }
        try {
            return DocumentsContract.getTreeDocumentId(tree);
        } catch (Exception e) {
            return folder;
        }
    }

    private static Uri root(Uri tree) {
        return DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree));
    }

    private static boolean reachable(Context ctx, String folder) {
        if (MOVIES.equals(folder)) {
            return Environment.MEDIA_MOUNTED.equals(Environment.getExternalStorageState()) && (Build.VERSION.SDK_INT >= 29
                || ctx.checkSelfPermission(android.Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED);
        }
        if (APP.equals(folder)) return appDir(ctx) != null;
        try (Cursor c = ctx.getContentResolver().query(root(Uri.parse(folder)), null, null, null, null)) {
            return c != null && c.moveToFirst();
        } catch (Exception e) {
            return false; // the stick was removed, or Fire OS changed its code
        }
    }

    /** The document called name in the tree's folder doc, or null. */
    private static String child(ContentResolver cr, Uri tree, String doc, String name) {
        try (Cursor c = cr.query(DocumentsContract.buildChildDocumentsUriUsingTree(tree, doc), new String[] {
            DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME}, null, null, null)) {
            while (c != null && c.moveToNext()) {
                if (name.equals(c.getString(1))) return c.getString(0);
            }
        }
        return null;
    }

    /** The file is already in the folder (a copy finished earlier). */
    static boolean exists(Context ctx, String folder, String name) {
        try {
            if (MOVIES.equals(folder) && Build.VERSION.SDK_INT >= 29) {
                int slash = name.lastIndexOf('/');
                // Only the app's own files: after a reinstall they are not seen.
                // Files, not Video: once complete the scan types .ogm and
                // .divx by extension, as no video.
                try (Cursor c = ctx.getContentResolver().query(MediaStore.Files.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY),
                    new String[] {MediaStore.MediaColumns._ID},
                    MediaStore.MediaColumns.RELATIVE_PATH + "=? AND " + MediaStore.MediaColumns.DISPLAY_NAME + "=?",
                    new String[] {"Movies/VodMax/" + name.substring(0, slash + 1), name.substring(slash + 1)}, null)) {
                    return c != null && c.moveToFirst();
                }
            }
            if (MOVIES.equals(folder)) return new File(moviesDir(), name).exists();
            if (APP.equals(folder)) {
                File d = appDir(ctx);
                return d != null && new File(d, name).exists();
            }
            if (folder.isEmpty()) return false;
            Uri tree = Uri.parse(folder);
            String doc = DocumentsContract.getTreeDocumentId(tree);
            for (String p : name.split("/")) {
                doc = child(ctx.getContentResolver(), tree, doc, p);
                if (doc == null) return false;
            }
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    /** Free bytes in the folder, or -1 when only the copy itself can tell (a tree: fstatvfs). */
    @SuppressWarnings("deprecation")
    static long free(Context ctx, String folder) {
        File dir = MOVIES.equals(folder) ? Environment.getExternalStorageDirectory() : APP.equals(folder) ? appDir(ctx) : null;
        if (dir == null) return -1;
        try {
            return new StatFs(dir.getPath()).getAvailableBytes();
        } catch (RuntimeException e) {
            return -1;
        }
    }

    private static long free(FileDescriptor fd) {
        try {
            StructStatVfs s = Os.fstatvfs(fd);
            return s.f_bavail * s.f_frsize;
        } catch (Exception e) {
            return -1;
        }
    }

    private static void deletePart(Context ctx, Copy c) {
        if (c.part.isEmpty()) return;
        try {
            if (!c.part.startsWith("content:")) new File(c.part).delete();
            else if (MOVIES.equals(c.folder)) ctx.getContentResolver().delete(Uri.parse(c.part), null, null);
            else DocumentsContract.deleteDocument(ctx.getContentResolver(), Uri.parse(c.part));
        } catch (Exception ignored) {
            // already gone, or the stick is out: Android drops a pending
            // MediaStore entry after a week
        }
    }

    /** The piece copied so far, or -1 when there is none. */
    private long length(Copy c) {
        if (c.part.isEmpty()) return -1;
        if (!c.part.startsWith("content:")) {
            File f = new File(c.part);
            return f.isFile() ? f.length() : -1;
        }
        try (ParcelFileDescriptor p = getContentResolver().openFileDescriptor(Uri.parse(c.part), "r")) {
            return p == null ? -1 : p.getStatSize();
        } catch (Exception e) {
            return -1;
        }
    }

    /** A new, empty piece: not seen as a film until it is complete. */
    private void create(Copy c) throws IOException {
        int slash = c.name.lastIndexOf('/');
        String dirs = c.name.substring(0, slash + 1), base = c.name.substring(slash + 1);
        if (MOVIES.equals(c.folder) && Build.VERSION.SDK_INT >= 29) {
            ContentValues v = new ContentValues();
            v.put(MediaStore.MediaColumns.DISPLAY_NAME, base);
            v.put(MediaStore.MediaColumns.MIME_TYPE, c.mime);
            v.put(MediaStore.MediaColumns.RELATIVE_PATH, "Movies/VodMax/" + dirs);
            v.put(MediaStore.MediaColumns.IS_PENDING, 1);
            Uri u = getContentResolver().insert(MediaStore.Video.Media.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY), v);
            if (u == null) throw new IOException("MediaStore");
            c.part = u.toString();
        } else if (MOVIES.equals(c.folder) || APP.equals(c.folder)) {
            File d = MOVIES.equals(c.folder) ? moviesDir() : appDir(this);
            if (d == null) throw new IOException("folder");
            File f = new File(d, c.name + ".part");
            File parent = f.getParentFile();
            if (!parent.isDirectory() && !parent.mkdirs()) throw new IOException("mkdirs");
            if (!f.isFile() && !f.createNewFile()) throw new IOException("create");
            c.part = f.getPath();
        } else {
            // DocumentsContract, not the documentfile library. As
            // application/octet-stream the provider adds no extension.
            ContentResolver cr = getContentResolver();
            Uri tree = Uri.parse(c.folder);
            String doc = DocumentsContract.getTreeDocumentId(tree);
            for (String p : dirs.isEmpty() ? new String[0] : dirs.substring(0, dirs.length() - 1).split("/")) {
                String next = child(cr, tree, doc, p);
                if (next == null) {
                    Uri made = DocumentsContract.createDocument(cr, DocumentsContract.buildDocumentUriUsingTree(tree, doc),
                        DocumentsContract.Document.MIME_TYPE_DIR, p);
                    if (made == null) throw new IOException("mkdir");
                    next = DocumentsContract.getDocumentId(made);
                }
                doc = next;
            }
            Uri f = DocumentsContract.createDocument(cr, DocumentsContract.buildDocumentUriUsingTree(tree, doc),
                "application/octet-stream", base + ".part");
            if (f == null) throw new IOException("create");
            c.part = f.toString();
        }
        synchronized (LOCK) {
            save(this);
        }
    }

    private FileOutputStream open(Copy c, boolean truncate) throws IOException {
        if (!c.part.startsWith("content:")) return new FileOutputStream(c.part, !truncate);
        ParcelFileDescriptor p = getContentResolver().openFileDescriptor(Uri.parse(c.part), truncate ? "wt" : "wa");
        if (p == null) throw new FileNotFoundException(c.part);
        return new ParcelFileDescriptor.AutoCloseOutputStream(p);
    }

    /** The complete piece becomes the film, visible to the Gallery, VLC and the TV. */
    private String finish(Copy c) {
        try {
            if (c.part.startsWith("content:") && MOVIES.equals(c.folder)) {
                ContentValues v = new ContentValues();
                v.put(MediaStore.MediaColumns.IS_PENDING, 0);
                if (getContentResolver().update(Uri.parse(c.part), v, null, null) != 1) return FAILED;
            } else if (c.part.startsWith("content:")) {
                String base = c.name.substring(c.name.lastIndexOf('/') + 1);
                Uri u = DocumentsContract.renameDocument(getContentResolver(), Uri.parse(c.part), base);
                if (u != null) c.part = u.toString();
            } else {
                File from = new File(c.part), to = new File(c.part.substring(0, c.part.length() - ".part".length()));
                if (!from.renameTo(to)) return reachable(this, c.folder) ? FAILED : FOLDER;
                c.part = to.getPath();
                if (MOVIES.equals(c.folder)) MediaScannerConnection.scanFile(this, new String[] {to.getPath()}, new String[] {c.mime}, null);
            }
        } catch (Exception e) {
            return reachable(this, c.folder) ? FAILED : FOLDER;
        }
        c.done = c.size;
        return DONE;
    }

    // ---- The network ----

    /** Null when a copy may go now, else the state to wait in. */
    static String network(Context ctx) {
        ConnectivityManager cm = ctx.getSystemService(ConnectivityManager.class);
        Network n = cm == null ? null : cm.getActiveNetwork();
        NetworkCapabilities caps = n == null ? null : cm.getNetworkCapabilities(n);
        if (caps == null) return NETWORK;
        if (roaming(cm, caps)) return WIFI;
        boolean mobile = ctx.getSharedPreferences(MainActivity.PREFS, MODE_PRIVATE).getBoolean(KEY_MOBILE, false);
        // Not the capability: before Android 9 a VPN's network is never
        // NOT_METERED, while this reads the network under it.
        if (cm.isActiveNetworkMetered() && !mobile) return WIFI;
        return null;
    }

    @SuppressWarnings("deprecation")
    private static boolean roaming(ConnectivityManager cm, NetworkCapabilities caps) {
        if (Build.VERSION.SDK_INT >= 28) return !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_ROAMING);
        android.net.NetworkInfo i = cm.getActiveNetworkInfo();
        return i != null && i.isRoaming();
    }

    private static HttpURLConnection request(String url, long from, String etag) throws IOException {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setInstanceFollowRedirects(false); // a proxy's login is never saved as the film
        c.setUseCaches(false);
        c.setConnectTimeout(15_000);
        c.setReadTimeout(60_000);
        c.setRequestProperty("User-Agent", "VodMax/" + BuildConfig.VERSION_NAME);
        // Android asks for gzip by default, which hides Content-Length.
        c.setRequestProperty("Accept-Encoding", "identity");
        if (from > 0) {
            c.setRequestProperty("Range", "bytes=" + from + "-");
            // The etag from the POST, not the one Cloudflare shows.
            c.setRequestProperty("If-Range", etag.startsWith("\"") ? etag : "\"" + etag + "\"");
        }
        return c;
    }

    // ---- The service ----

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        if (Build.VERSION.SDK_INT >= 26) {
            getSystemService(NotificationManager.class).createNotificationChannel(
                new NotificationChannel(CHANNEL, getString(R.string.copy_channel), NotificationManager.IMPORTANCE_LOW));
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        try {
            if (Build.VERSION.SDK_INT >= 29) startForeground(NOTE, note(null), ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
            else startForeground(NOTE, note(null));
        } catch (RuntimeException e) {
            stopSelf(); // Android 15: the 6 hours are used up
            return START_NOT_STICKY;
        }
        synchronized (LOCK) {
            lastStart = startId;
            if (worker == null) {
                workers++;
                worker = new Thread(this::work, "copy");
                worker.start();
            }
        }
        // Not restarted by Android: the app restarts the queue when it opens.
        return START_NOT_STICKY;
    }

    /**
     * Android 15 gives dataSync services 6 hours a day: the copy stops
     * cleanly, paused, and goes on when the app opens (which resets them).
     */
    @Override
    public void onTimeout(int startId, int fgsType) {
        halt();
        stopForeground(STOP_FOREGROUND_REMOVE);
        stopSelf();
    }

    @Override
    public void onDestroy() {
        halt();
        super.onDestroy();
    }

    private void halt() {
        stopping = true;
        HttpURLConnection c = connection;
        // A read blocked on the socket ends now; not on the main thread.
        if (c != null) new Thread(c::disconnect).start();
    }

    // No timeout: a copy takes hours, and the worker ends, releasing the
    // locks, about 30 minutes after its last data (paused).
    @SuppressLint("WakelockTimeout")
    private void work() {
        // A foreground service keeps the process, not the CPU or Wi-Fi: a
        // box going to standby mid-copy would stall it until it paused.
        PowerManager.WakeLock wake = getSystemService(PowerManager.class).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "vodmax:copy");
        WifiManager wm = getApplicationContext().getSystemService(WifiManager.class);
        int mode = wifiLockMode(Build.VERSION.SDK_INT);
        WifiManager.WifiLock wifi = wm != null && mode != 0 ? wm.createWifiLock(mode, "vodmax:copy") : null;
        wake.acquire();
        try {
            if (wifi != null) wifi.acquire();
            copyAll();
        } finally {
            if (wifi != null && wifi.isHeld()) wifi.release();
            wake.release();
        }
        main.post(() -> {
            int id;
            synchronized (LOCK) {
                if (worker != null) return; // a copy was queued meanwhile
                id = lastStart;
            }
            stopForeground(STOP_FOREGROUND_REMOVE);
            stopSelfResult(id); // not if a start is on its way
        });
    }

    private void copyAll() {
        Copy last = null;
        while (true) {
            Copy c;
            synchronized (LOCK) {
                // 30 minutes without data: the copies after it wait too, so
                // that they go in the order asked (resume() restarts them all).
                if (last != null && PAUSED.equals(last.state) && !stopping) {
                    for (Copy o : list(this)) {
                        if (QUEUED.equals(o.state)) o.state = PAUSED;
                    }
                    save(this);
                }
                c = null;
                if (!stopping) {
                    for (Copy o : list(this)) {
                        if (QUEUED.equals(o.state)) {
                            c = o;
                            break;
                        }
                    }
                }
                if (c == null) {
                    worker = null;
                    workers--;
                    break;
                }
                current = c;
                c.state = COPYING; // no other worker takes it
            }
            copy(c);
            current = null;
            last = c;
        }
    }

    private void copy(Copy c) {
        lastData = SystemClock.elapsedRealtime();
        int tries = 0;
        while (true) {
            if (c.cancelled) {
                deletePart(this, c);
                return;
            }
            if (stopping || paused(lastData, SystemClock.elapsedRealtime())) {
                set(c, PAUSED);
                return;
            }
            String wait = network(this);
            if (wait != null) {
                set(c, wait);
                sleep(c, 5_000);
                continue;
            }
            gotData = false;
            String r;
            try {
                r = attempt(c);
            } catch (IOException e) {
                r = NETWORK;
            } catch (RuntimeException e) {
                // the content provider of the folder failed: gone, or for good
                r = reachable(this, c.folder) ? FAILED : FOLDER;
            } finally {
                connection = null;
            }
            if (gotData) tries = 0;
            if (RESTART.equals(r)) continue;
            if (NETWORK.equals(r) || FOLDER.equals(r)) {
                set(c, r);
                sleep(c, backoff(tries++));
                continue;
            }
            if (c.cancelled) continue;
            if (CHANGED.equals(r) || FAT32.equals(r)) deletePart(this, c);
            set(c, r);
            return;
        }
    }

    /** One try: a final state, or NETWORK, FOLDER, RESTART to go on. */
    private String attempt(Copy c) throws IOException {
        if (!reachable(this, c.folder)) return FOLDER;
        long have = length(c); // the real length, not a saved number
        if (have > c.size) {
            deletePart(this, c);
            have = -1;
        }
        if (have < 0) {
            try {
                create(c);
            } catch (Exception e) {
                return reachable(this, c.folder) ? FAILED : FOLDER; // refused for good (read-only, a name it rejects), or gone
            }
            have = 0;
        }
        c.done = have;
        if (have == c.size) return finish(c);
        set(c, COPYING);
        HttpURLConnection conn = request(c.url, have, c.etag);
        connection = conn;
        try {
            String what = outcome(conn.getResponseCode(), have, c.size, conn.getHeaderFieldLong("Content-Length", -1),
                conn.getHeaderField("Content-Range"));
            if (RESTART.equals(what)) {
                deletePart(this, c);
                c.part = "";
                return RESTART;
            }
            if (!WRITE.equals(what) && !APPEND.equals(what)) return what;
            String failed = write(c, conn, WRITE.equals(what) ? 0 : have);
            if (failed != null) return failed;
        } finally {
            conn.disconnect();
        }
        long now = length(c);
        if (now == c.size) return finish(c);
        return now > c.size ? RESTART : NETWORK; // the connection ended early
    }

    /** Writes the answer from pos: null when it ended, or why it stopped. */
    private String write(Copy c, HttpURLConnection conn, long pos) throws IOException {
        FileOutputStream out;
        try {
            out = open(c, pos == 0);
        } catch (IOException | RuntimeException e) {
            return reachable(this, c.folder) ? FAILED : FOLDER;
        }
        try (InputStream in = conn.getInputStream()) {
            long free = free(out.getFD());
            if (free >= 0 && free < c.size - pos) return SPACE;
            byte[] buf = new byte[256 * 1024];
            for (int n; !c.cancelled && !stopping && (n = in.read(buf)) > 0; ) {
                n = (int) Math.min(n, c.size - pos);
                if (n <= 0) break;
                try {
                    out.write(buf, 0, n);
                } catch (IOException e) {
                    if (fat32(pos, n)) return FAT32;
                    return String.valueOf(e.getMessage()).contains("ENOSPC") ? SPACE : FOLDER;
                }
                pos += n;
                c.done = pos;
                gotData = true;
                lastData = SystemClock.elapsedRealtime();
                if (lastData - lastNote > 1000) {
                    lastNote = lastData;
                    show(c);
                }
            }
        } finally {
            try {
                out.close();
            } catch (IOException ignored) {
                // the final length check sees what was not written
            }
        }
        return null;
    }

    private void sleep(Copy c, long ms) {
        long end = SystemClock.elapsedRealtime() + ms;
        while (!c.cancelled && !stopping && SystemClock.elapsedRealtime() < end) {
            SystemClock.sleep(500);
        }
    }

    private void set(Copy c, String state) {
        boolean changed;
        synchronized (LOCK) {
            changed = !state.equals(c.state);
            c.state = state;
            if (changed) save(this);
        }
        if (changed) show(c);
    }

    private void show(Copy c) {
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm != null && !stopping) nm.notify(NOTE, note(c));
    }

    /**
     * The progress on the phone. If notifications are refused, the copy goes
     * on all the same; the page's «Su questo dispositivo» shows it.
     */
    @SuppressWarnings("deprecation")
    private Notification note(Copy c) {
        Notification.Builder b = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        b.setSmallIcon(android.R.drawable.stat_sys_download).setOngoing(true).setOnlyAlertOnce(true)
            .setContentIntent(PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class), PendingIntent.FLAG_IMMUTABLE));
        if (c == null) return b.setContentTitle(getString(R.string.copy_channel)).build();
        b.setContentTitle(c.title.isEmpty() ? c.name : c.title);
        if (WIFI.equals(c.state)) b.setContentText(getString(R.string.copy_wifi));
        else if (NETWORK.equals(c.state)) b.setContentText(getString(R.string.copy_network));
        else if (FOLDER.equals(c.state)) b.setContentText(getString(R.string.copy_folder));
        else {
            int pct = (int) (c.done * 100 / Math.max(1, c.size));
            b.setProgress(100, pct, false).setContentText(pct + "%");
        }
        return b.build();
    }
}

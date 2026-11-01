package app.vodmax.client;

import android.app.Activity;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;

import androidx.core.content.FileProvider;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.security.MessageDigest;

/**
 * Updates the app from the project's GitHub releases: downloads the APK the
 * server announced, checks its SHA-256 against the release's checksums and
 * hands it to the Android installer, where the user confirms. Android only
 * accepts it when it is signed with the same key as the installed app.
 */
final class AppUpdater {
    /** Only the project's release files are downloaded. */
    static final String RELEASES = "https://github.com/silirim/VodMax/releases/download/";

    interface Listener {
        void progress(int percent);

        /** null when the installer was opened (or the permission asked). */
        void done(String error);

        /** Something to do before it goes on (not an error). */
        void note(String text);
    }

    private final Activity activity;
    private File pending;
    private boolean waitingPermission;

    AppUpdater(Activity activity) {
        this.activity = activity;
    }

    void start(String url, String sha256, String version, Listener listener) {
        if (url == null || !url.startsWith(RELEASES) || sha256 == null || !sha256.matches("[0-9a-f]{64}")) {
            listener.done(activity.getString(R.string.update_refused));
            return;
        }
        new Thread(() -> {
            try {
                File dir = new File(activity.getCacheDir(), "updates");
                if (!dir.isDirectory() && !dir.mkdirs()) throw new IOException("cache");
                File[] old = dir.listFiles();
                if (old != null) for (File f : old) f.delete();
                File apk = new File(dir, "VodMax-" + version.replaceAll("[^0-9A-Za-z.]", "") + ".apk");
                download(url, apk, sha256, listener);
                activity.runOnUiThread(() -> {
                    pending = apk;
                    String error = install();
                    if (waitingPermission) listener.note(activity.getString(R.string.update_allow_installs));
                    else listener.done(error);
                });
            } catch (Exception e) {
                activity.runOnUiThread(() -> listener.done(String.valueOf(e.getMessage())));
            }
        }).start();
    }

    // GitHub answers with a redirect to its file host, both https: followed
    // by HttpURLConnection.
    private void download(String url, File to, String sha256, Listener listener) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(15000);
        c.setReadTimeout(30000);
        c.setRequestProperty("User-Agent", "VodMax/" + BuildConfig.VERSION_NAME);
        try {
            if (c.getResponseCode() != 200) throw new IOException("HTTP " + c.getResponseCode());
            long total = c.getContentLengthLong();
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            try (InputStream in = c.getInputStream(); OutputStream out = new FileOutputStream(to)) {
                byte[] buf = new byte[64 * 1024];
                long done = 0;
                int last = -1;
                for (int n; (n = in.read(buf)) > 0; ) {
                    out.write(buf, 0, n);
                    md.update(buf, 0, n);
                    done += n;
                    int pct = total > 0 ? (int) (done * 100 / total) : 0;
                    if (pct != last) {
                        last = pct;
                        int p = pct;
                        activity.runOnUiThread(() -> listener.progress(p));
                    }
                }
            }
            StringBuilder hex = new StringBuilder();
            for (byte b : md.digest()) hex.append(String.format("%02x", b));
            if (!hex.toString().equals(sha256)) {
                to.delete();
                throw new IOException(activity.getString(R.string.update_bad_checksum));
            }
        } finally {
            c.disconnect();
        }
    }

    /**
     * Opens the installer, after asking to allow installing apps if needed.
     * Returns an error for the page, or null. Some TVs and Fire TVs lack
     * these system screens: that is said instead of closing the app.
     */
    private String install() {
        if (pending == null) return null;
        if (Build.VERSION.SDK_INT >= 26 && !activity.getPackageManager().canRequestPackageInstalls()) {
            waitingPermission = true;
            try {
                activity.startActivity(new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + activity.getPackageName())));
            } catch (Exception e) {
                try {
                    activity.startActivity(new Intent(Settings.ACTION_SECURITY_SETTINGS));
                } catch (Exception again) {
                    waitingPermission = false;
                    return activity.getString(R.string.update_no_permission_screen);
                }
            }
            return null;
        }
        try {
            Uri uri = FileProvider.getUriForFile(activity, activity.getPackageName() + ".updates", pending);
            activity.startActivity(new Intent(Intent.ACTION_VIEW)
                .setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK));
        } catch (Exception e) {
            return activity.getString(R.string.update_no_installer);
        }
        return null;
    }

    /** Back from the permission screen: install if it was granted. */
    void resume() {
        if (!waitingPermission) return;
        if (Build.VERSION.SDK_INT >= 26 && !activity.getPackageManager().canRequestPackageInstalls()) return;
        waitingPermission = false;
        install(); // the installer opens; nothing left to tell the page
    }
}

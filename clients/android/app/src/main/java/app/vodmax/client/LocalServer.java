package app.vodmax.client;

import android.content.Context;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.io.ByteArrayOutputStream;
import java.io.FileInputStream;
import java.util.TimeZone;

/**
 * The VodMax server inside the app, for this device alone ("-device":
 * no recordings, no pause of live TV). It is the same Go program as the
 * home server, shipped as libvodmax.so so that Android installs it as an
 * executable; it listens on the loopback only.
 */
final class LocalServer {
    static final String URL = "http://127.0.0.1:8095";
    private static Process process;

    private LocalServer() {
    }

    /** Starts the server if it is not running; waits for it. Returns the admin token. */
    static synchronized String start(Context context) throws Exception {
        File data = new File(context.getFilesDir(), "server");
        // Only the process started here is trusted: anything else answering
        // on the port (another app) would be handed the admin token.
        if (process == null || exited(process)) {
            if (running()) throw new Exception(context.getString(R.string.local_port_busy));
            if (process != null) process.destroy();
            File bin = new File(context.getApplicationInfo().nativeLibraryDir, "libvodmax.so");
            ProcessBuilder pb = new ProcessBuilder(bin.getAbsolutePath(), "-device", "-data", data.getAbsolutePath(),
                "-listen", "127.0.0.1:8095");
            // Local times (the nightly sync, the guide) need the zone.
            pb.environment().put("TZ", TimeZone.getDefault().getID());
            pb.redirectErrorStream(true);
            process = pb.start();
            File log = new File(context.getFilesDir(), "server.log");
            Process p = process;
            new Thread(() -> copyLog(p.getInputStream(), log)).start();
            long until = System.currentTimeMillis() + 30000;
            while (!running()) {
                if (exited(p)) throw new Exception(context.getString(R.string.local_exited, p.exitValue()));
                if (System.currentTimeMillis() > until) throw new Exception(context.getString(R.string.local_no_answer));
                Thread.sleep(300);
            }
        }
        try (InputStream in = new FileInputStream(new File(data, "admin.token"))) {
            return new String(readAll(in), StandardCharsets.UTF_8).trim();
        }
    }

    static synchronized void stop() {
        if (process != null) process.destroy();
        process = null;
    }

    /** True when the server has at least one subscription. */
    static boolean configured(String token) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(URL + "/api/v1/subscriptions").openConnection();
            c.setRequestProperty("Authorization", "Bearer " + token);
            c.setConnectTimeout(3000);
            c.setReadTimeout(5000);
            if (c.getResponseCode() != 200) return false;
            byte[] body = readAll(c.getInputStream());
            c.disconnect();
            return !new String(body, StandardCharsets.UTF_8).trim().equals("[]");
        } catch (Exception e) {
            return false;
        }
    }

    static boolean running() {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(URL + "/api/v1/health").openConnection();
            c.setConnectTimeout(1000);
            c.setReadTimeout(2000);
            int code = c.getResponseCode();
            c.disconnect();
            return code == 200;
        } catch (Exception e) {
            return false;
        }
    }

    // Process.isAlive and InputStream.readAllBytes need newer Androids.
    private static boolean exited(Process p) {
        try {
            p.exitValue();
            return true;
        } catch (IllegalThreadStateException running) {
            return false;
        }
    }

    private static byte[] readAll(InputStream in) throws java.io.IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[8192];
        for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
        return out.toByteArray();
    }

    // The last run's output, for when something goes wrong.
    private static void copyLog(InputStream in, File log) {
        try (OutputStream out = new FileOutputStream(log, false)) {
            byte[] buf = new byte[8192];
            for (int n; (n = in.read(buf)) > 0; ) {
                out.write(buf, 0, n);
                out.flush();
            }
        } catch (Exception ignored) {
        }
    }
}

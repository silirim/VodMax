package app.vodmax.client;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.BroadcastReceiver;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ActivityInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.content.res.Configuration;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.view.inputmethod.EditorInfo;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.EditText;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Locale;

/**
 * The VodMax app: the server's interface in a WebView (the same one as in
 * the browser), with films, episodes, recordings and catch-up played by
 * the native player (Media3), which reads formats a browser cannot.
 * First run: the server address; the device is then paired with a code in
 * the interface itself. Or no server at all: the app runs its own, for
 * this device alone (LocalServer).
 */
public class MainActivity extends Activity {
    static final String PREFS = "vodmax";
    private static final String KEY_SERVER = "server";
    private static final String KEY_LOCAL = "local";
    private static final String KEY_LOCAL_TOKEN = "localToken";
    private static final int PLAY = 1;
    private static final int PICK = 2;
    private static final int SAVE = 3;
    private static final int FOLDER = 4;
    private static final int WRITE_PERMISSION = 5;
    private static final int NOTIFY_PERMISSION = 6;
    private static final String KEY_ASKED_NOTIFY = "copyAskedNotifications";

    private WebView web;
    private String server;
    private final AppUpdater updater = new AppUpdater(this);
    // A file the page is choosing (VPN configuration, backup to restore),
    // and one it is saving (backup).
    private ValueCallback<Uri[]> picking;
    private byte[] saving;
    // A copy waiting for the folder choice or the storage permission.
    private volatile CopyService.Copy pendingCopy;
    private boolean signingIn;
    // A page of the server did not load: trying again before giving up.
    private boolean reconnecting;
    private String retried;
    private long retriedAt;
    // The sign-in is the one of a proxy in front of the server: Back there
    // cannot go back to the server (it would send us to the login again).
    private boolean proxyLogin;
    // Live TV plays in the page: the page says when (Bridge.pip) and the
    // whole activity becomes the small window.
    private boolean pipAvailable, pipPlaying;
    private int pipWidth, pipHeight;
    private BroadcastReceiver pipToggle;
    // The page's video in full screen (live TV, the browser player): the
    // WebView hands over a view to lay over everything.
    private View fullView;
    private WebChromeClient.CustomViewCallback fullDone;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        pipAvailable = Pip.available(this);
        if (pipAvailable) pipToggle = Pip.listen(this, () -> page("window.VodMaxNativePipToggle && window.VodMaxNativePipToggle()"));
        LocalServer.onExit = () -> runOnUiThread(() -> {
            if (!isDestroyed() && prefs().getBoolean(KEY_LOCAL, false)) openLocal();
        });
        server = prefs().getString(KEY_SERVER, null);
        if (server != null) server = normalize(server); // saved by older versions as typed
        if (prefs().getBoolean(KEY_LOCAL, false)) openLocal();
        else if (server == null) showSetup(null);
        else open("/");
    }

    private SharedPreferences prefs() {
        return getSharedPreferences(PREFS, MODE_PRIVATE);
    }

    // ---- First run: the server address ----

    private void showSetup(String message) {
        dropWeb();
        View setup = getLayoutInflater().inflate(R.layout.setup, null);
        setContentView(insetted(setup));
        EditText field = findViewById(R.id.server);
        TextView text = findViewById(R.id.setup_text);
        Button connect = findViewById(R.id.connect);
        if (server != null && !server.equals(LocalServer.URL)) field.setText(server);
        if (message != null) text.setText(message);
        Runnable go = () -> {
            String url = normalize(field.getText().toString());
            if (url == null) return;
            connect.setEnabled(false);
            text.setText(R.string.setup_checking);
            new Thread(() -> {
                boolean ok = reachable(url);
                runOnUiThread(() -> {
                    connect.setEnabled(true);
                    if (!ok) {
                        text.setText(R.string.setup_unreachable);
                        return;
                    }
                    LocalServer.stop();
                    server = url;
                    prefs().edit().putString(KEY_SERVER, url).putBoolean(KEY_LOCAL, false).apply();
                    open("/");
                });
            }).start();
        };
        connect.setOnClickListener(v -> go.run());
        findViewById(R.id.standalone).setOnClickListener(v -> {
            // Choosing it again hands the admin token to the page once more:
            // the way back to admin when the device token in it was revoked,
            // locked or its PIN forgotten (no other device exists here).
            prefs().edit().putBoolean(KEY_LOCAL, true).remove(KEY_LOCAL_TOKEN).apply();
            openLocal();
        });
        field.setOnEditorActionListener((v, action, event) -> {
            if (action == EditorInfo.IME_ACTION_GO || action == EditorInfo.IME_ACTION_DONE) {
                go.run();
                return true;
            }
            return false;
        });
        field.requestFocus();
    }

    /**
     * "192.168.1.10:8095" → "http://192.168.1.10:8095"; null if not an
     * address. In the form the WebView reports URLs in (lower-case scheme
     * and host, no default port): navigations are told apart from other
     * sites by comparing with it, and "http://NAS:80" would match nothing.
     */
    static String normalize(String s) {
        s = s.trim();
        if (s.isEmpty()) return null;
        String lower = s.toLowerCase(Locale.ROOT);
        if (!lower.startsWith("http://") && !lower.startsWith("https://")) s = "http://" + s;
        Uri u = Uri.parse(s);
        String scheme = u.getScheme().toLowerCase(Locale.ROOT), host = u.getHost();
        if (host == null || host.isEmpty()) return null;
        int port = u.getPort();
        boolean defaultPort = port == -1 || (port == 80 && scheme.equals("http")) || (port == 443 && scheme.equals("https"));
        String path = u.getEncodedPath() == null ? "" : u.getEncodedPath();
        while (path.endsWith("/")) path = path.substring(0, path.length() - 1);
        return scheme + "://" + host.toLowerCase(Locale.ROOT) + (defaultPort ? "" : ":" + port) + path;
    }

    private static boolean reachable(String base) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(base + "/api/v1/health").openConnection();
            c.setConnectTimeout(5000);
            c.setReadTimeout(5000);
            int code = c.getResponseCode();
            c.disconnect();
            return code == 200;
        } catch (Exception e) {
            return false;
        }
    }

    // ---- This device alone: the server inside the app ----

    private void openLocal() {
        dropWeb();
        setContentView(insetted(note(R.string.local_starting)));
        new Thread(() -> {
            try {
                String token = LocalServer.start(this);
                boolean configured = LocalServer.configured(token);
                runOnUiThread(() -> {
                    server = LocalServer.URL;
                    // First start: straight to the subscription to add.
                    String path = configured ? "/" : "/settings.html?view=subscriptions";
                    // The page keeps the token once it has it.
                    if (!token.equals(prefs().getString(KEY_LOCAL_TOKEN, null))) {
                        path += "#token=" + token;
                        prefs().edit().putString(KEY_LOCAL_TOKEN, token).apply();
                    }
                    open(path);
                });
            } catch (Exception e) {
                runOnUiThread(() -> {
                    // The choice stays: a busy port passes, next time it may work.
                    showSetup(getString(R.string.local_failed) + ": " + e.getMessage());
                });
            }
        }).start();
    }

    // ---- The interface ----

    @SuppressLint("SetJavaScriptEnabled")
    private void open(String path) {
        // Two reopenings at once (the local server's exit and a page error)
        // must not leave the first WebView running off screen.
        dropWeb();
        signingIn = proxyLogin = false; // the new WebView starts with the bridge
        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setUserAgentString(s.getUserAgentString() + " VodMaxAndroid/" + BuildConfig.VERSION_NAME);
        web.setBackgroundColor(0xFF0B0C10);
        web.addJavascriptInterface(new Bridge(), "VodMaxNative");
        web.setWebChromeClient(new WebChromeClient() {
            // <input type=file> does nothing in a WebView without this.
            @Override
            public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> callback, FileChooserParams params) {
                if (picking != null) picking.onReceiveValue(null);
                picking = callback;
                try {
                    startActivityForResult(pickIntent(), PICK);
                } catch (Exception e) {
                    picking = null;
                    callback.onReceiveValue(null);
                    return true;
                }
                return true;
            }

            // Without these the page's full-screen button does nothing in the app.
            @Override
            public void onShowCustomView(View v, CustomViewCallback done) {
                if (fullView != null) {
                    done.onCustomViewHidden();
                    return;
                }
                fullView = v;
                fullDone = done;
                ((FrameLayout) getWindow().getDecorView()).addView(v, new FrameLayout.LayoutParams(
                    FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
                PlayerActivity.immersive(MainActivity.this, true);
                if (!PlayerActivity.tv(MainActivity.this)) setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE);
                v.requestFocus();
            }

            @Override
            public void onHideCustomView() {
                leaveFullScreen();
            }
        });
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri u = request.getUrl();
                String url = u.toString();
                // The server sends http to https (Cloudflare, a proxy): from
                // now on the address is the https one, else its pages would
                // look like another site's login and the app would lose its
                // bridge (and Back would go to the first screen).
                String secure = request.isForMainFrame() && request.isRedirect() ? upgrade(server, url) : null;
                if (secure != null) {
                    server = secure;
                    if (!prefs().getBoolean(KEY_LOCAL, false)) prefs().edit().putString(KEY_SERVER, server).apply();
                }
                // The home login goes to the account's site and comes back
                // with a code: the whole round trip stays in the app.
                if (url.startsWith(server + "/auth/sso/")) {
                    signingIn = true;
                    web.removeJavascriptInterface("VodMaxNative"); // not for the account's site
                }
                if (url.startsWith(server + "/") || url.equals(server)) {
                    if (serverPage(server, url)) endSignIn();
                    return false;
                }
                // A login in front of the server (Cloudflare Access, Authelia…)
                // redirects to its own site: it stays in the app, so its cookie
                // ends up where the interface runs, until it sends us back.
                if (request.isForMainFrame() && request.isRedirect() && !signingIn) {
                    signingIn = true;
                    proxyLogin = true;
                    web.removeJavascriptInterface("VodMaxNative");
                }
                if (signingIn) return false;
                // Trailers and other sites open outside the app.
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, u));
                } catch (Exception ignored) {
                }
                return true;
            }

            // A page that has no video to keep on screen.
            @Override
            public void onPageStarted(WebView view, String url, Bitmap favicon) {
                updatePip(false, 0, 0);
                // Back on the server after a login the check above did not
                // see: WebView does not report a form's POST (Cloudflare
                // Access sends the code that way) nor where it redirects.
                // The page started without the bridge: loaded again with it.
                if (view == web && signingIn && serverPage(server, url)) {
                    endSignIn();
                    // Not a page carrying a one-time code in its #fragment
                    // (the home login's): reloading could lose it.
                    if (!url.contains("#")) view.post(view::reload);
                }
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) showOffline(request.getUrl().toString());
            }
        });
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        setContentView(insetted(web));
        web.requestFocus(View.FOCUS_DOWN);
        web.loadUrl(server + path);
    }

    /**
     * Android 15 draws apps under the status and navigation bars: the
     * content is kept clear of them (and of a camera cutout).
     */
    private View insetted(View content) {
        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(0xFF0B0C10);
        root.addView(content, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets i = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout()
                    | WindowInsets.Type.ime());
                v.setPadding(i.left, i.top, i.right, i.bottom);
                return WindowInsets.CONSUMED;
            }
            v.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
            return insets.consumeSystemWindowInsets();
        });
        root.setFitsSystemWindows(false);
        return root;
    }

    private void leaveFullScreen() {
        if (fullView == null) return;
        ((FrameLayout) getWindow().getDecorView()).removeView(fullView);
        fullView = null;
        fullDone = null;
        PlayerActivity.immersive(this, false);
        setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED);
        if (web != null) web.requestFocus();
    }

    private void dropWeb() {
        leaveFullScreen();
        if (web != null) {
            web.destroy();
            web = null;
        }
    }

    private TextView note(int text) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextColor(0xFFA9ABB5);
        t.setTextSize(20);
        t.setGravity(android.view.Gravity.CENTER);
        return t;
    }

    /**
     * The server restarts (an update) or the Wi-Fi is still coming back
     * after standby: four tries over at least 15 s, keeping the page, before
     * the first screen, which loses it.
     */
    private void showOffline(String url) {
        if (prefs().getBoolean(KEY_LOCAL, false)) {
            openLocal();
            return;
        }
        if (reconnecting || web == null) return;
        // The server answers but this page still fails: no endless retries.
        if (url.equals(retried) && android.os.SystemClock.elapsedRealtime() - retriedAt < 10_000) {
            showSetup(getString(R.string.error_title) + ": " + server + ". " + getString(R.string.setup_unreachable));
            return;
        }
        reconnecting = true;
        WebView w = web;
        String base = server;
        FrameLayout root = (FrameLayout) w.getParent();
        TextView wait = note(R.string.reconnecting);
        root.addView(wait, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
        w.setVisibility(View.INVISIBLE); // not the WebView's own error page
        new Thread(() -> {
            boolean ok = false;
            for (int pause : new int[] {1000, 2000, 4000, 8000}) {
                try {
                    Thread.sleep(pause);
                } catch (InterruptedException e) {
                    break;
                }
                ok = reachable(base);
                if (ok) break;
            }
            boolean back = ok;
            runOnUiThread(() -> {
                reconnecting = false;
                if (web != w) return; // another page or the first screen meanwhile
                if (!back) {
                    showSetup(getString(R.string.error_title) + ": " + server + ". " + getString(R.string.setup_unreachable));
                    return;
                }
                root.removeView(wait);
                w.setVisibility(View.VISIBLE);
                retried = url;
                retriedAt = android.os.SystemClock.elapsedRealtime();
                w.loadUrl(url);
            });
        }).start();
    }

    /** Called by the page: window.VodMaxNative. */
    private class Bridge {
        @JavascriptInterface
        public String version() {
            return BuildConfig.VERSION_NAME;
        }

        /** {url, title, start (s), live, next (title of the next episode),
         *  markers {intro_start, intro_end, credits}} → the native player. */
        @JavascriptInterface
        public void play(String json) {
            runOnUiThread(() -> {
                try {
                    JSONObject o = new JSONObject(json);
                    Intent i = new Intent(MainActivity.this, PlayerActivity.class);
                    String url = o.getString("url");
                    if (url.startsWith("/")) url = server + url;
                    i.putExtra(PlayerActivity.URL, url);
                    i.putExtra(PlayerActivity.TITLE, o.optString("title"));
                    i.putExtra(PlayerActivity.START, (long) (o.optDouble("start", 0) * 1000));
                    i.putExtra(PlayerActivity.LIVE, o.optBoolean("live", false));
                    i.putExtra(PlayerActivity.EPISODE, o.optBoolean("episode", false));
                    i.putExtra(PlayerActivity.NEXT, o.optString("next", ""));
                    i.putExtra(PlayerActivity.AUDIO_LANGUAGE, o.optString("audio_language", ""));
                    JSONObject pr = o.optJSONObject("progress");
                    if (pr != null) {
                        i.putExtra(PlayerActivity.PROGRESS_URL, server + pr.optString("url"));
                        i.putExtra(PlayerActivity.PROGRESS_TOKEN, pr.optString("token"));
                        i.putExtra(PlayerActivity.PROGRESS_PROFILE, pr.optString("profile"));
                    }
                    JSONObject rs = o.optJSONObject("restart");
                    if (rs != null) {
                        String r = rs.optString("url");
                        i.putExtra(PlayerActivity.RESTART_URL, r.startsWith("/") ? server + r : r);
                        i.putExtra(PlayerActivity.RESTART_OFFSET, (long) (rs.optDouble("offset", 0) * 1000));
                        i.putExtra(PlayerActivity.KNOWN_DURATION, (long) (rs.optDouble("duration", 0) * 1000));
                        i.putExtra(PlayerActivity.GROWS, rs.optBoolean("grows", false));
                    }
                    JSONObject m = o.optJSONObject("markers");
                    if (m != null) {
                        i.putExtra(PlayerActivity.INTRO_START, m.optInt("intro_start", 0));
                        i.putExtra(PlayerActivity.INTRO_END, m.optInt("intro_end", 0));
                        i.putExtra(PlayerActivity.CREDITS, m.optInt("credits", 0));
                    }
                    PlayerActivity.closeRunning();
                    startActivityForResult(i, PLAY);
                } catch (Exception e) {
                    web.evaluateJavascript("window.VodMaxNativeError && window.VodMaxNativeError("
                        + JSONObject.quote(String.valueOf(e.getMessage())) + ")", null);
                }
            });
        }

        /** The page's video started or stopped; its size in pixels. */
        @JavascriptInterface
        public void pip(boolean playing, int width, int height) {
            runOnUiThread(() -> updatePip(playing, width, height));
        }

        /** {url, sha256, version} of a release APK: download, check, install. */
        @JavascriptInterface
        public void update(String json) {
            runOnUiThread(() -> {
                try {
                    JSONObject o = new JSONObject(json);
                    updater.start(o.optString("url"), o.optString("sha256"), o.optString("version"), new AppUpdater.Listener() {
                        @Override
                        public void progress(int percent) {
                            if (web != null) web.evaluateJavascript("window.VodMaxUpdateProgress && window.VodMaxUpdateProgress(" + percent + ")", null);
                        }

                        @Override
                        public void note(String text) {
                            if (web != null) web.evaluateJavascript("window.VodMaxUpdateNote && window.VodMaxUpdateNote("
                                + JSONObject.quote(text) + ")", null);
                        }

                        @Override
                        public void done(String error) {
                            if (web != null) web.evaluateJavascript("window.VodMaxUpdateDone && window.VodMaxUpdateDone("
                                + (error == null ? "null" : JSONObject.quote(error)) + ")", null);
                        }
                    });
                } catch (Exception e) {
                    if (web != null) web.evaluateJavascript("window.VodMaxUpdateDone && window.VodMaxUpdateDone("
                        + JSONObject.quote(String.valueOf(e.getMessage())) + ")", null);
                }
            });
        }

        /** A file made by the page (a backup): the person chooses where it goes. */
        @JavascriptInterface
        public void saveFile(String name, String mime, String base64) {
            runOnUiThread(() -> {
                try {
                    saving = android.util.Base64.decode(base64, android.util.Base64.DEFAULT);
                    Intent i = new Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
                        .setType(mime).putExtra(Intent.EXTRA_TITLE, name);
                    startActivityForResult(i, SAVE);
                } catch (Exception | OutOfMemoryError e) {
                    saving = null;
                    saveDone(getString(R.string.save_unavailable));
                }
            });
        }

        /** Copies to this device (0056): "phone", "tv" with a stick or disk in, else "". */
        @JavascriptInterface
        public String copyTarget() {
            return MainActivity.this.copyTarget();
        }

        /**
         * {url, name, mime, size, etag, title} from POST /api/v1/save/…:
         * "" (queued), "folder" or "permission" (a choice is open: the outcome
         * comes to window.VodMaxCopyDone(code)), "space", "already",
         * "copying", "invalid".
         */
        @JavascriptInterface
        public String copy(String json) {
            CopyService.Copy c = null;
            try {
                JSONObject o = new JSONObject(json);
                c = CopyService.fromPage(server, o.optString("url"), o.optString("name"), o.optString("mime"),
                    o.optLong("size"), o.optString("etag"), o.optString("title"));
            } catch (Exception ignored) {
            }
            if (c == null || copyTarget().isEmpty()) return "invalid";
            return place(c);
        }

        /** The folder picker; true: back to Movies/VodMax (phone). */
        @JavascriptInterface
        public void chooseFolder(boolean reset) {
            runOnUiThread(() -> {
                if (!reset) pickFolder();
                else if (!tvDevice()) prefs().edit().remove(CopyService.KEY_FOLDER).apply();
            });
        }

        /** "Copia anche su rete mobile", for this device. */
        @JavascriptInterface
        public void copyMobile(boolean on) {
            prefs().edit().putBoolean(CopyService.KEY_MOBILE, on).apply();
        }

        /**
         * {folder, kind ("movies", "tree", "app", "" none yet), mobile,
         * copies: [{id, title, name, size, done, percent, state, finished}]}.
         */
        @JavascriptInterface
        public String copies() {
            try {
                String folder = copyFolder();
                JSONArray list = new JSONArray();
                for (CopyService.Copy c : CopyService.snapshot(MainActivity.this)) {
                    list.put(new JSONObject().put("id", c.id).put("title", c.title).put("name", c.name).put("size", c.size)
                        .put("done", c.done).put("percent", (int) (c.done * 100 / Math.max(1, c.size))).put("state", c.state)
                        .put("finished", CopyService.finished(c.state)));
                }
                String kind = folder.isEmpty() || folder.equals(CopyService.MOVIES) || folder.equals(CopyService.APP) ? folder : "tree";
                return new JSONObject().put("folder", CopyService.folderName(MainActivity.this, folder)).put("kind", kind)
                    .put("mobile", prefs().getBoolean(CopyService.KEY_MOBILE, false)).put("copies", list).toString();
            } catch (Exception e) {
                return "{\"folder\":\"\",\"kind\":\"\",\"mobile\":false,\"copies\":[]}";
            }
        }

        /** Annulla a copy not done (its piece is deleted), or Togli a done one. */
        @JavascriptInterface
        public void cancelCopy(String id) {
            CopyService.cancel(MainActivity.this, id);
        }

        @JavascriptInterface
        public void changeServer() {
            runOnUiThread(() -> showSetup(null));
        }

        /** Is there a file picker? Many TVs have none, or a stub (0057). */
        @JavascriptInterface
        public boolean canPickFile() {
            ResolveInfo r = getPackageManager().resolveActivity(pickIntent(), 0);
            return r != null && r.activityInfo != null && !r.activityInfo.packageName.equals("com.google.android.tv.frameworkpackagestubs");
        }
    }

    /** Any type: the one of a .m3u or a .conf changes from app to app. */
    private static Intent pickIntent() {
        return new Intent(Intent.ACTION_GET_CONTENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
    }

    private void page(String js) {
        if (web != null) web.evaluateJavascript(js, null);
    }

    // ---- Copies to this device (CopyService) ----

    private boolean tvDevice() {
        return PlayerActivity.tv(this) || getPackageManager().hasSystemFeature(PackageManager.FEATURE_LEANBACK);
    }

    private String copyTarget() {
        if (prefs().getBoolean(KEY_LOCAL, false)) return ""; // this device alone: its files are already here
        if (!tvDevice()) return "phone";
        return CopyService.removable(this) ? "tv" : "";
    }

    /** The folder in use: Movies/VodMax by default on the phone, none on a TV. */
    private String copyFolder() {
        String f = prefs().getString(CopyService.KEY_FOLDER, "");
        return f.isEmpty() && !tvDevice() ? CopyService.MOVIES : f;
    }

    /** Queues c, or opens what it needs first (any thread). */
    private String place(CopyService.Copy c) {
        String folder = copyFolder();
        if (folder.isEmpty()) {
            pendingCopy = c;
            runOnUiThread(this::pickFolder);
            return "folder";
        }
        if (folder.equals(CopyService.MOVIES) && Build.VERSION.SDK_INT < 29
            && checkSelfPermission(android.Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            pendingCopy = c;
            runOnUiThread(() -> requestPermissions(new String[] {android.Manifest.permission.WRITE_EXTERNAL_STORAGE}, WRITE_PERMISSION));
            return "permission";
        }
        c.folder = folder;
        String code = CopyService.add(this, c);
        if (code.isEmpty()) runOnUiThread(() -> {
            CopyService.resume(this); // copies paused earlier go first
            if (Build.VERSION.SDK_INT >= 33 && !prefs().getBoolean(KEY_ASKED_NOTIFY, false)
                && checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                prefs().edit().putBoolean(KEY_ASKED_NOTIFY, true).apply();
                requestPermissions(new String[] {android.Manifest.permission.POST_NOTIFICATIONS}, NOTIFY_PERMISSION);
            }
        });
        return code;
    }

    /** After the choice: the pending copy goes on, and the page hears how. */
    private void placePending() {
        CopyService.Copy c = pendingCopy;
        pendingCopy = null;
        if (c == null) return;
        new Thread(() -> {
            String code = place(c);
            if (!code.equals("folder") && !code.equals("permission")) runOnUiThread(() -> copyDone(code));
        }).start();
    }

    /** The outcome of a copy that waited for a choice; "folder"/"permission" when refused. */
    private void copyDone(String code) {
        page("window.VodMaxCopyDone && window.VodMaxCopyDone(" + JSONObject.quote(code) + ")");
    }

    /**
     * Android's folder picker. Missing on some boxes (Fire OS 7, Android TV
     * with only Google's stub): the app's folder on the stick, after saying
     * that uninstalling VodMax deletes it.
     */
    private void pickFolder() {
        ResolveInfo r = getPackageManager().resolveActivity(new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), 0);
        if (r != null && r.activityInfo != null && !r.activityInfo.packageName.equals("com.google.android.tv.frameworkpackagestubs")) {
            try {
                startActivityForResult(new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE), FOLDER);
                return;
            } catch (Exception ignored) {
            }
        }
        if (!tvDevice()) {
            if (pendingCopy != null) copyDone("folder");
            pendingCopy = null;
            return;
        }
        new AlertDialog.Builder(this).setMessage(R.string.copy_app_folder)
            .setPositiveButton(android.R.string.ok, (d, w) -> {
                prefs().edit().putString(CopyService.KEY_FOLDER, CopyService.APP).apply();
                placePending();
            })
            .setNegativeButton(R.string.cancel, null)
            .setOnDismissListener(d -> {
                if (pendingCopy == null) return;
                pendingCopy = null;
                copyDone("folder");
            })
            .show();
    }

    private void updatePip(boolean playing, int width, int height) {
        pipPlaying = playing;
        pipWidth = width;
        pipHeight = height;
        if (pipAvailable && Build.VERSION.SDK_INT >= 26) setPictureInPictureParams(Pip.params(this, playing, width, height));
    }

    @Override
    protected void onUserLeaveHint() {
        super.onUserLeaveHint();
        if (pipAvailable && pipPlaying && Build.VERSION.SDK_INT >= 26 && Build.VERSION.SDK_INT < 31) Pip.enter(this, true, pipWidth, pipHeight);
    }

    @Override
    public void onPictureInPictureModeChanged(boolean on, Configuration config) {
        super.onPictureInPictureModeChanged(on, config);
        page("window.VodMaxNativePip && window.VodMaxNativePip(" + on + ")");
    }

    /** A page of the server, not its home sign-in (which goes on to the account's site). */
    static boolean serverPage(String server, String url) {
        if (server == null || url == null) return false;
        boolean ours = url.equals(server) || url.startsWith(server + "/") || url.startsWith(server + "?") || url.startsWith(server + "#");
        return ours && !url.startsWith(server + "/auth/sso/");
    }

    /** The https form of an http server address when url is on it; else null. */
    static String upgrade(String server, String url) {
        if (server == null || url == null || !server.startsWith("http://")) return null;
        String secure = "https://" + server.substring("http://".length());
        return url.equals(secure) || url.startsWith(secure + "/") ? secure : null;
    }

    private void endSignIn() {
        if (!signingIn) return;
        signingIn = false;
        proxyLogin = false;
        web.addJavascriptInterface(new Bridge(), "VodMaxNative");
    }

    private void saveDone(String error) {
        if (web != null) web.evaluateJavascript("window.VodMaxSaveDone && window.VodMaxSaveDone("
            + (error == null ? "null" : JSONObject.quote(error)) + ")", null);
    }

    @Override
    protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (request == PICK) {
            if (picking != null) picking.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result, data));
            picking = null;
            return;
        }
        if (request == FOLDER) {
            Uri tree = result == RESULT_OK && data != null ? data.getData() : null;
            try {
                // shortcut: earlier folders keep their permission (queued copies may use them); release them if the 128-grant limit bites.
                if (tree != null) getContentResolver().takePersistableUriPermission(tree,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
            } catch (SecurityException e) {
                tree = null;
            }
            if (tree == null) {
                if (pendingCopy != null) copyDone("folder");
                pendingCopy = null;
                return;
            }
            String folder = tree.toString();
            prefs().edit().putString(CopyService.KEY_FOLDER, folder).apply();
            new Thread(() -> CopyService.moveWaiting(this, folder)).start();
            placePending();
            return;
        }
        if (request == SAVE) {
            byte[] bytes = saving;
            saving = null;
            if (result != RESULT_OK || data == null || data.getData() == null || bytes == null) {
                saveDone(getString(R.string.save_cancelled));
                return;
            }
            try (java.io.OutputStream out = getContentResolver().openOutputStream(data.getData())) {
                out.write(bytes);
            } catch (Exception e) {
                saveDone(getString(R.string.save_failed));
                return;
            }
            saveDone(null); // written and closed
            return;
        }
        if (request != PLAY || web == null) return;
        if (data != null && data.getBooleanExtra(PlayerActivity.SUPERSEDED, false)) return;
        long position = data == null ? 0 : data.getLongExtra(PlayerActivity.POSITION, 0);
        long duration = data == null ? 0 : data.getLongExtra(PlayerActivity.DURATION, 0);
        String error = data == null ? null : data.getStringExtra(PlayerActivity.ERROR);
        boolean next = data != null && data.getBooleanExtra(PlayerActivity.NEXT, false);
        boolean ended = data != null && data.getBooleanExtra(PlayerActivity.ENDED, false);
        int introStart = data == null ? 0 : data.getIntExtra(PlayerActivity.INTRO_START, 0);
        int introEnd = data == null ? 0 : data.getIntExtra(PlayerActivity.INTRO_END, 0);
        int credits = data == null ? 0 : data.getIntExtra(PlayerActivity.CREDITS, 0);
        web.evaluateJavascript("window.VodMaxNativeDone && window.VodMaxNativeDone({position:" + position / 1000.0
            + ",duration:" + duration / 1000.0 + ",error:" + (error == null ? "null" : JSONObject.quote(error))
            + ",next:" + next + ",ended:" + ended + ",intro_start:" + introStart + ",intro_end:" + introEnd + ",credits:" + credits + "})", null);
    }

    // Back: the page decides (close a panel, go to the menu); with nothing
    // to close, the app goes to the background.
    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        // Back leaves full screen first, as in a browser.
        if (keyCode == KeyEvent.KEYCODE_BACK && fullDone != null) {
            WebChromeClient.CustomViewCallback done = fullDone;
            leaveFullScreen();
            done.onCustomViewHidden();
            return true;
        }
        if (keyCode == KeyEvent.KEYCODE_BACK && web != null && signingIn) {
            // Back on the account's site: the sign-in is given up. On the
            // login of a proxy, the server is out of reach without it: back
            // to the first screen, where another address can be given.
            boolean proxy = proxyLogin;
            endSignIn();
            if (proxy && !serverPage(server, web.getUrl())) showSetup(null);
            else web.loadUrl(server + "/");
            return true;
        }
        if (keyCode == KeyEvent.KEYCODE_BACK && web != null) {
            web.evaluateJavascript("(window.VodMaxNativeBack ? window.VodMaxNativeBack() : false)", handled -> {
                if (!"true".equals(handled)) moveTaskToBack(true);
            });
            return true;
        }
        return super.onKeyDown(keyCode, event);
    }

    @Override
    public void onRequestPermissionsResult(int request, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(request, permissions, results);
        if (request != WRITE_PERMISSION || pendingCopy == null) return;
        if (results.length > 0 && results[0] == PackageManager.PERMISSION_GRANTED) placePending();
        else {
            pendingCopy = null;
            copyDone("permission");
        }
    }

    // Android may stop the server while the app is in the background.
    @Override
    protected void onResume() {
        super.onResume();
        updater.resume();
        CopyService.resume(this); // copies stopped with the app, or paused, go on
        if (!prefs().getBoolean(KEY_LOCAL, false) || web == null) return;
        new Thread(() -> {
            if (!LocalServer.ours()) runOnUiThread(this::openLocal);
        }).start();
    }

    // The login cookie of a proxy in front of the server must survive the app being closed.
    @Override
    protected void onPause() {
        super.onPause();
        CookieManager.getInstance().flush();
    }

    @Override
    protected void onDestroy() {
        if (pipToggle != null) unregisterReceiver(pipToggle);
        if (web != null) web.destroy();
        super.onDestroy();
    }
}

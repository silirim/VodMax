package app.vodmax.client;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.view.inputmethod.EditorInfo;
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

import org.json.JSONObject;

import java.net.HttpURLConnection;
import java.net.URL;

/**
 * The VodMax app: the server's interface in a WebView (the same one as in
 * the browser), with films, episodes, recordings and catch-up played by
 * the native player (Media3), which reads formats a browser cannot.
 * First run: the server address; the device is then paired with a code in
 * the interface itself. Or no server at all: the app runs its own, for
 * this device alone (LocalServer).
 */
public class MainActivity extends Activity {
    private static final String PREFS = "vodmax";
    private static final String KEY_SERVER = "server";
    private static final String KEY_LOCAL = "local";
    private static final String KEY_LOCAL_TOKEN = "localToken";
    private static final int PLAY = 1;
    private static final int PICK = 2;
    private static final int SAVE = 3;

    private WebView web;
    private String server;
    private final AppUpdater updater = new AppUpdater(this);
    // A file the page is choosing (VPN configuration, backup to restore),
    // and one it is saving (backup).
    private ValueCallback<Uri[]> picking;
    private byte[] saving;
    private boolean signingIn;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        server = prefs().getString(KEY_SERVER, null);
        if (prefs().getBoolean(KEY_LOCAL, false)) openLocal();
        else if (server == null) showSetup(null);
        else open("/");
    }

    private SharedPreferences prefs() {
        return getSharedPreferences(PREFS, MODE_PRIVATE);
    }

    // ---- First run: the server address ----

    private void showSetup(String message) {
        if (web != null) {
            web.destroy();
            web = null;
        }
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
            prefs().edit().putBoolean(KEY_LOCAL, true).apply();
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

    /** "192.168.1.10:8095" → "http://192.168.1.10:8095"; null if not an address. */
    static String normalize(String s) {
        s = s.trim();
        if (s.isEmpty()) return null;
        if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://" + s;
        while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
        Uri u = Uri.parse(s);
        return u.getHost() == null || u.getHost().isEmpty() ? null : s;
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
        if (web != null) {
            web.destroy();
            web = null;
        }
        TextView wait = new TextView(this);
        wait.setText(R.string.local_starting);
        wait.setTextColor(0xFFA9ABB5);
        wait.setTextSize(20);
        wait.setGravity(android.view.Gravity.CENTER);
        setContentView(insetted(wait));
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
                    prefs().edit().putBoolean(KEY_LOCAL, false).apply();
                    showSetup(getString(R.string.local_failed) + ": " + e.getMessage());
                });
            }
        }).start();
    }

    // ---- The interface ----

    @SuppressLint("SetJavaScriptEnabled")
    private void open(String path) {
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
                Intent i = new Intent(Intent.ACTION_GET_CONTENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*");
                try {
                    startActivityForResult(i, PICK);
                } catch (Exception e) {
                    picking = null;
                    callback.onReceiveValue(null);
                    return false;
                }
                return true;
            }
        });
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri u = request.getUrl();
                // The home login goes to the account's site and comes back
                // with a code: the whole round trip stays in the app.
                if (u.toString().startsWith(server + "/auth/sso/")) signingIn = true;
                if (u.toString().startsWith(server + "/") || u.toString().equals(server)) {
                    if (!u.getPath().startsWith("/auth/sso/")) signingIn = false;
                    return false;
                }
                if (signingIn) return false;
                // Trailers and other sites open outside the app.
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, u));
                } catch (Exception ignored) {
                }
                return true;
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) showOffline();
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

    private void showOffline() {
        if (prefs().getBoolean(KEY_LOCAL, false)) {
            openLocal();
            return;
        }
        showSetup(getString(R.string.error_title) + ": " + server + ". " + getString(R.string.setup_unreachable));
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
                    }
                    JSONObject m = o.optJSONObject("markers");
                    if (m != null) {
                        i.putExtra(PlayerActivity.INTRO_START, m.optInt("intro_start", 0));
                        i.putExtra(PlayerActivity.INTRO_END, m.optInt("intro_end", 0));
                        i.putExtra(PlayerActivity.CREDITS, m.optInt("credits", 0));
                    }
                    startActivityForResult(i, PLAY);
                } catch (Exception e) {
                    web.evaluateJavascript("window.VodMaxNativeError && window.VodMaxNativeError("
                        + JSONObject.quote(String.valueOf(e.getMessage())) + ")", null);
                }
            });
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
                } catch (Exception e) {
                    saving = null;
                    saveDone(getString(R.string.save_unavailable));
                }
            });
        }

        @JavascriptInterface
        public void changeServer() {
            runOnUiThread(() -> showSetup(null));
        }
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
        if (request == SAVE) {
            byte[] bytes = saving;
            saving = null;
            if (result != RESULT_OK || data == null || data.getData() == null || bytes == null) {
                saveDone(getString(R.string.save_cancelled));
                return;
            }
            try (java.io.OutputStream out = getContentResolver().openOutputStream(data.getData())) {
                out.write(bytes);
                saveDone(null);
            } catch (Exception e) {
                saveDone(getString(R.string.save_failed));
            }
            return;
        }
        if (request != PLAY || web == null) return;
        long position = data == null ? 0 : data.getLongExtra(PlayerActivity.POSITION, 0);
        long duration = data == null ? 0 : data.getLongExtra(PlayerActivity.DURATION, 0);
        String error = data == null ? null : data.getStringExtra(PlayerActivity.ERROR);
        boolean next = data != null && data.getBooleanExtra(PlayerActivity.NEXT, false);
        int introStart = data == null ? 0 : data.getIntExtra(PlayerActivity.INTRO_START, 0);
        int introEnd = data == null ? 0 : data.getIntExtra(PlayerActivity.INTRO_END, 0);
        int credits = data == null ? 0 : data.getIntExtra(PlayerActivity.CREDITS, 0);
        web.evaluateJavascript("window.VodMaxNativeDone && window.VodMaxNativeDone({position:" + position / 1000.0
            + ",duration:" + duration / 1000.0 + ",error:" + (error == null ? "null" : JSONObject.quote(error))
            + ",next:" + next + ",intro_start:" + introStart + ",intro_end:" + introEnd + ",credits:" + credits + "})", null);
    }

    // Back: the page decides (close a panel, go to the menu); with nothing
    // to close, the app goes to the background.
    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        if (keyCode == KeyEvent.KEYCODE_BACK && web != null) {
            web.evaluateJavascript("(window.VodMaxNativeBack ? window.VodMaxNativeBack() : false)", handled -> {
                if (!"true".equals(handled)) moveTaskToBack(true);
            });
            return true;
        }
        return super.onKeyDown(keyCode, event);
    }

    // Android may stop the server while the app is in the background.
    @Override
    protected void onResume() {
        super.onResume();
        updater.resume();
        if (!prefs().getBoolean(KEY_LOCAL, false) || web == null) return;
        new Thread(() -> {
            if (!LocalServer.running()) runOnUiThread(this::openLocal);
        }).start();
    }

    @Override
    protected void onDestroy() {
        if (web != null) web.destroy();
        super.onDestroy();
    }
}

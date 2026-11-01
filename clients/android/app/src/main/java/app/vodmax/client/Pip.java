package app.vodmax.client;

import android.app.Activity;
import android.app.PendingIntent;
import android.app.PictureInPictureParams;
import android.app.RemoteAction;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.drawable.Icon;
import android.os.Build;
import android.util.Rational;

import androidx.annotation.RequiresApi;
import androidx.core.content.ContextCompat;

import java.util.Collections;

/**
 * The video in a small window while the person uses other apps. Phones and
 * tablets only: on a TV, Home keeps pausing it as before.
 */
final class Pip {
    private static final String TOGGLE = BuildConfig.APPLICATION_ID + ".PIP_TOGGLE";

    private Pip() {
    }

    static boolean available(Activity a) {
        return Build.VERSION.SDK_INT >= 26
            && a.getPackageManager().hasSystemFeature(PackageManager.FEATURE_PICTURE_IN_PICTURE)
            && (a.getResources().getConfiguration().uiMode & Configuration.UI_MODE_TYPE_MASK) != Configuration.UI_MODE_TYPE_TELEVISION;
    }

    /** Android refuses a window more extreme than 2.39:1, either way. */
    @RequiresApi(26)
    static PictureInPictureParams params(Activity a, boolean playing, int width, int height) {
        PictureInPictureParams.Builder b = new PictureInPictureParams.Builder();
        if (width > 0 && height > 0) {
            float max = 2.39f;
            int w = Math.min(width, (int) (height * max)), h = Math.min(height, (int) (width * max));
            b.setAspectRatio(new Rational(w, h));
        }
        PendingIntent toggle = PendingIntent.getBroadcast(a, 0, new Intent(TOGGLE).setPackage(a.getPackageName()),
            PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        String label = a.getString(playing ? R.string.pip_pause : R.string.pip_play);
        Icon icon = Icon.createWithResource(a, playing ? R.drawable.ic_pip_pause : R.drawable.ic_pip_play);
        b.setActions(Collections.singletonList(new RemoteAction(icon, label, label, toggle)));
        // From Android 12 the window opens with the gesture home; the
        // activities ask for it themselves on older versions.
        if (Build.VERSION.SDK_INT >= 31) b.setAutoEnterEnabled(playing);
        return b.build();
    }

    /** The window's play/pause button. To unregister in onDestroy. */
    static BroadcastReceiver listen(Activity a, Runnable toggle) {
        BroadcastReceiver r = new BroadcastReceiver() {
            @Override
            public void onReceive(Context c, Intent i) {
                toggle.run();
            }
        };
        ContextCompat.registerReceiver(a, r, new IntentFilter(TOGGLE), ContextCompat.RECEIVER_NOT_EXPORTED);
        return r;
    }

    /** False when Android or the person's settings refuse the window. */
    @RequiresApi(26)
    static boolean enter(Activity a, boolean playing, int width, int height) {
        try {
            return a.enterPictureInPictureMode(params(a, playing, width, height));
        } catch (IllegalStateException | IllegalArgumentException e) {
            return false;
        }
    }
}

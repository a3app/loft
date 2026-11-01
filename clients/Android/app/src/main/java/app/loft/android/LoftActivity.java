/*
 * Android client, shared code: the base class for every screen (activity)
 * in the phone and TV builds. It applies the in-app language choice from
 * Lang before any layout is inflated, and keeps every screen clear of the
 * status bar, the display cutout (camera hole), the navigation bar and the
 * on-screen keyboard.
 */
package app.loft.android;

import android.content.Context;
import android.view.View;
import android.view.ViewGroup;

import androidx.appcompat.app.AppCompatActivity;
import androidx.core.graphics.Insets;
import androidx.core.view.ViewCompat;
import androidx.core.view.WindowInsetsCompat;

/**
 * Base class for the app's screens. Screens extend this instead of
 * AppCompatActivity directly.
 *
 * <p><b>Language.</b> Applies the Prefs language before inflate so XML
 * layouts and getString() share one locale.
 *
 * <p><b>System bars.</b> The app targets Android 16 (targetSdk 36). From
 * Android 15 on, such an app is always drawn edge to edge: the window
 * extends under the status bar and the navigation bar, and the app itself
 * must keep its content out from under them (the old way to opt out is
 * ignored at targetSdk 36). Without that, a screen's top row sat under the
 * clock and the signal icons, where it could not even be tapped. On older
 * Android versions the system still keeps the window inside the bars, the
 * insets reaching the screen are zero, and nothing here changes anything.
 */
public abstract class LoftActivity extends AppCompatActivity {
    /** Swaps in a context with the chosen locale; runs before onCreate(), so every string resource follows it. */
    @Override
    protected void attachBaseContext(Context newBase) {
        super.attachBaseContext(Lang.wrap(newBase));
    }

    /**
     * Called by Android after every setContentView. Pads the screen's root
     * view by the size of the system bars and the display cutout, so its
     * own background still runs under the bars (no odd stripe of another
     * colour there) while its content starts below them.
     *
     * <p>The bottom padding also covers the on-screen keyboard: an edge to
     * edge window is no longer shrunk for the keyboard by the system, so a
     * text field near the bottom would otherwise end up under it.
     *
     * <p>The root's own padding from the layout is remembered once and the
     * bar sizes are added to it, so repeated inset updates (rotation, the
     * keyboard opening) never pile up.
     */
    @Override
    public void onContentChanged() {
        super.onContentChanged();
        ViewGroup content = findViewById(android.R.id.content);
        if (content == null || content.getChildCount() == 0) {
            return;
        }
        View root = content.getChildAt(0);
        final int l = root.getPaddingLeft();
        final int t = root.getPaddingTop();
        final int r = root.getPaddingRight();
        final int b = root.getPaddingBottom();
        final boolean barBelow = bottomBarHandlesInsets();
        ViewCompat.setOnApplyWindowInsetsListener(root, (v, in) -> {
            Insets bars = in.getInsets(WindowInsetsCompat.Type.systemBars()
                    | WindowInsetsCompat.Type.displayCutout());
            Insets ime = in.getInsets(WindowInsetsCompat.Type.ime());
            // A screen with a Material bottom bar lets that bar pad itself for
            // the navigation bar (it does so on its own); the root then only
            // makes room for the part of the keyboard taller than that.
            int bottom = barBelow
                    ? Math.max(0, ime.bottom - bars.bottom)
                    : Math.max(bars.bottom, ime.bottom);
            v.setPadding(l + bars.left, t + bars.top, r + bars.right, b + bottom);
            if (!barBelow) {
                // Everything is handled here; views below must not pad again
                // (e.g. a layout that also says fitsSystemWindows).
                return WindowInsetsCompat.CONSUMED;
            }
            // Pass on only the bottom navigation-bar inset, for the bottom bar.
            return new WindowInsetsCompat.Builder(in)
                    .setInsets(WindowInsetsCompat.Type.systemBars()
                            | WindowInsetsCompat.Type.displayCutout(),
                            Insets.of(0, 0, 0, ime.bottom > bars.bottom ? 0 : bars.bottom))
                    .build();
        });
        ViewCompat.requestApplyInsets(root);
    }

    /**
     * True for a screen whose layout ends in a Material bottom navigation
     * bar, which adds the navigation-bar height to its own padding. Such a
     * screen gets top and side padding here, and the bottom inset is left to
     * the bar so it is not added twice.
     */
    protected boolean bottomBarHandlesInsets() {
        return false;
    }
}

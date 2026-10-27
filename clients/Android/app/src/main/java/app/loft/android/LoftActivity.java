/*
 * Android client, shared code: the base class for every screen (activity)
 * in the phone and TV builds. Its only job is to apply the in-app language
 * choice from Lang before any layout is inflated.
 */
package app.loft.android;

import android.content.Context;

import androidx.appcompat.app.AppCompatActivity;

/** Applies Prefs language before inflate so XML and getString share one locale. */
public abstract class LoftActivity extends AppCompatActivity {
    @Override
    /** Swaps in a context with the chosen locale; runs before onCreate(), so every string resource follows it. */
    protected void attachBaseContext(Context newBase) {
        super.attachBaseContext(Lang.wrap(newBase));
    }
}

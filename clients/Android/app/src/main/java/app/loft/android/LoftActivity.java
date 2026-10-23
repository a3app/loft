package app.loft.android;

import android.content.Context;

import androidx.appcompat.app.AppCompatActivity;

/** Applies Prefs language before inflate so XML and getString share one locale. */
public abstract class LoftActivity extends AppCompatActivity {
    @Override
    protected void attachBaseContext(Context newBase) {
        super.attachBaseContext(Lang.wrap(newBase));
    }
}

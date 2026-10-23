package app.loft.android;

import android.app.Application;

public final class LoftApp extends Application {
    private Profiles profiles;

    @Override
    public void onCreate() {
        super.onCreate();
        Lang.apply(this);
        profiles = new Profiles(this);
    }

    public Profiles profiles() {
        if (profiles == null) {
            profiles = new Profiles(this);
        }
        return profiles;
    }

    static LoftApp of(android.content.Context c) {
        return (LoftApp) c.getApplicationContext();
    }
}

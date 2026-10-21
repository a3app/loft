package app.loft.android;

import android.app.Activity;
import android.content.Intent;
import android.net.VpnService;
import android.os.Bundle;
import android.widget.Button;
import android.widget.EditText;
import android.widget.TextView;

/** Thin host: paste YAML or a share URI, Connect / Disconnect. */
public final class MainActivity extends Activity {
    private static final int REQ_VPN = 1;
    private EditText body;
    private TextView status;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);
        body = findViewById(R.id.body);
        status = findViewById(R.id.status);
        body.setText(SessionStore.yaml(this));
        Button on = findViewById(R.id.connect);
        Button off = findViewById(R.id.disconnect);
        on.setOnClickListener(v -> connect());
        off.setOnClickListener(v -> stopService(new Intent(this, LoftVpnService.class)));
    }

    private void connect() {
        SessionStore.setYaml(this, body.getText().toString());
        Intent prep = VpnService.prepare(this);
        if (prep != null) {
            startActivityForResult(prep, REQ_VPN);
            return;
        }
        startService(new Intent(this, LoftVpnService.class));
        status.setText("Connecting…");
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        if (requestCode == REQ_VPN && resultCode == RESULT_OK) {
            startService(new Intent(this, LoftVpnService.class));
            status.setText("Connecting…");
        }
    }
}

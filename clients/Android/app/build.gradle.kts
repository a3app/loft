plugins {
    id("com.android.application")
}

android {
    namespace = "app.loft.android"
    compileSdk = 36
    defaultConfig {
        applicationId = "app.loft.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 6
        versionName = "1.0.6"
    }
    buildTypes {
        release {
            isMinifyEnabled = false
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }
}

dependencies {
    implementation(files("../../mobile/build/engine.aar"))
}

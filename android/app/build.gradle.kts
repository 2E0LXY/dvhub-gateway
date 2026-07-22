plugins {
    id("com.android.application")
}

android {
    namespace = "uk.co.dvhub.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "uk.co.dvhub.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 3
        versionName = "1.0.2"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    buildFeatures {
        viewBinding = true
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += setOf("META-INF/AL2.0", "META-INF/LGPL2.1")
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.17.0")
    implementation("androidx.appcompat:appcompat:1.7.1")
    implementation("androidx.activity:activity-ktx:1.11.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-ktx:2.9.3")
    implementation("androidx.lifecycle:lifecycle-livedata-ktx:2.9.3")
    implementation("androidx.recyclerview:recyclerview:1.4.0")
    implementation("com.google.android.material:material:1.13.0")
    implementation("com.squareup.okhttp3:okhttp:5.1.0")
    implementation("com.google.code.gson:gson:2.13.1")

    testImplementation("junit:junit:4.13.2")
    testImplementation("com.squareup.okhttp3:mockwebserver:5.1.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test.espresso:espresso-core:3.7.0")
}

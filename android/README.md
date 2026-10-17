# DVHub Remote for Android

Native Android controller and secure voice client for the 2E0LXY DVHub gateway. It uses authenticated HTTPS and one-use WebSocket tickets; it does not embed gateway or radio-network passwords.

## Download

**[Download the latest Yorkshire Link HUB APK](https://github.com/2E0LXY/dvhub-gateway/releases)**

## Install

1. Download the release APK, or build `app/build/outputs/apk/debug/app-debug.apk`, and copy it to an Android 8.0 or newer device.
2. Permit installation from the browser or file manager used to open it.
3. In **Settings**, choose **Administrator** for AI and full system controls, or **Radio user** for voice-only access. Enter the issued login, callsign and DMR ID, then tap **Save encrypted & connect**.
4. A radio user opens **Link**, allows microphone access, and holds PTT to transmit. Release PTT to receive.

Gateway credentials are encrypted with a non-exportable Android Keystore key. Passwords entered for individual or 60-second test connections are used only for the current screen/session and cleared afterward. Permanent conference mode uses the gateway's protected server-side configuration and never downloads those network passwords to Android.

## Radio operation

- Select a network; its complete server-side talkgroup list is downloaded into the talkgroup selector.
- Native app PTT and speaker RX use 8 kHz PCM through private AllStar node 1997 and the managed TG23530 conference. A single-talker lock and server watchdog prevent stuck transmissions.
- The TG 23530 bridge screen can start a 60-second test or permanent server-managed conference and has an explicit disconnect-all control.
- The status screen configures and controls native AllStar node 530471 and reports linked nodes without exposing its node password.
- P25, NXDN and DV30 health are visible in the multimode summary.
- One or two allowlisted DV30/DV3000 devices provide hardware AMBE conversion through the bounded local broker. Busy or unavailable hardware fails closed.

Use radio-network access only in accordance with the licence conditions and network policies applicable to your callsign.

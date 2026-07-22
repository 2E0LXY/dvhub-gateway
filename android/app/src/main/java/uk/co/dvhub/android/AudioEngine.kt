package uk.co.dvhub.android

import android.annotation.SuppressLint
import android.media.*
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

class AudioEngine(private val transmit: (ByteArray) -> Unit) {
    private val transmitting = AtomicBoolean(false)
    private var record: AudioRecord? = null
    private val output = AudioTrack.Builder()
        .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
        .setAudioFormat(AudioFormat.Builder().setEncoding(AudioFormat.ENCODING_PCM_16BIT).setSampleRate(48_000).setChannelMask(AudioFormat.CHANNEL_OUT_MONO).build())
        .setBufferSizeInBytes(AudioTrack.getMinBufferSize(48_000, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(4096))
        .setTransferMode(AudioTrack.MODE_STREAM).build().apply { play() }

    @SuppressLint("MissingPermission")
    fun startTx(): Boolean {
        if (!transmitting.compareAndSet(false, true)) return true
        val size = AudioRecord.getMinBufferSize(48_000, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(4096)
        record = AudioRecord(MediaRecorder.AudioSource.VOICE_COMMUNICATION, 48_000, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, size)
        if (record?.state != AudioRecord.STATE_INITIALIZED) { transmitting.set(false); return false }
        record?.startRecording()
        thread(name = "DVHub-Microphone") {
            val frame = ByteArray(320)
            while (transmitting.get()) {
                val read = record?.read(frame, 0, frame.size, AudioRecord.READ_BLOCKING) ?: -1
                if (read == frame.size) transmit(frame.copyOf())
                else if (read > 0) transmit(frame.copyOf(read))
            }
        }
        return true
    }

    fun stopTx() {
        if (!transmitting.getAndSet(false)) return
        runCatching { record?.stop() }; record?.release(); record = null
    }

    fun play(bytes: ByteArray) { if (!transmitting.get() && bytes.isNotEmpty()) output.write(bytes, 0, bytes.size, AudioTrack.WRITE_NON_BLOCKING) }
    fun release() { stopTx(); runCatching { output.stop() }; output.release() }
}

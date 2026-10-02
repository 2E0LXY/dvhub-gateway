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
        .setAudioFormat(AudioFormat.Builder().setEncoding(AudioFormat.ENCODING_PCM_16BIT).setSampleRate(PcmAudio.DEVICE_RATE).setChannelMask(AudioFormat.CHANNEL_OUT_MONO).build())
        .setBufferSizeInBytes(AudioTrack.getMinBufferSize(PcmAudio.DEVICE_RATE, AudioFormat.CHANNEL_OUT_MONO, AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(4096))
        .setTransferMode(AudioTrack.MODE_STREAM).build().apply { play() }

    @SuppressLint("MissingPermission")
    fun startTx(): Boolean {
        if (!transmitting.compareAndSet(false, true)) return true
        val size = AudioRecord.getMinBufferSize(PcmAudio.DEVICE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT).coerceAtLeast(4096)
        record = AudioRecord(MediaRecorder.AudioSource.VOICE_COMMUNICATION, PcmAudio.DEVICE_RATE, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT, size)
        if (record?.state != AudioRecord.STATE_INITIALIZED) { transmitting.set(false); return false }
        record?.startRecording()
        thread(name = "DVHub-Microphone") {
            val frame = ShortArray(PcmAudio.DEVICE_SAMPLES_PER_FRAME)
            var used = 0
            while (transmitting.get()) {
                val read = record?.read(frame, used, frame.size - used, AudioRecord.READ_BLOCKING) ?: -1
                if (read <= 0) continue
                used += read
                if (used == frame.size) {
                    transmit(PcmAudio.downsample48kTo8k(frame))
                    used = 0
                }
            }
        }
        return true
    }

    fun stopTx() {
        if (!transmitting.getAndSet(false)) return
        runCatching { record?.stop() }; record?.release(); record = null
    }

    fun play(bytes: ByteArray) {
        if (!transmitting.get() && bytes.size == PcmAudio.GATEWAY_BYTES_PER_FRAME) {
            val samples = PcmAudio.upsample8kTo48k(bytes)
            output.write(samples, 0, samples.size, AudioTrack.WRITE_NON_BLOCKING)
        }
    }
    fun release() { stopTx(); runCatching { output.stop() }; output.release() }
}

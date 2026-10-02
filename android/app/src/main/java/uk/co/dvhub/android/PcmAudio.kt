package uk.co.dvhub.android

import java.nio.ByteBuffer
import java.nio.ByteOrder

/** Converts Android's 48 kHz PCM stream to the gateway's 8 kHz/20 ms frames. */
object PcmAudio {
    const val DEVICE_RATE = 48_000
    const val GATEWAY_RATE = 8_000
    const val DEVICE_SAMPLES_PER_FRAME = 960
    const val GATEWAY_SAMPLES_PER_FRAME = 160
    const val GATEWAY_BYTES_PER_FRAME = GATEWAY_SAMPLES_PER_FRAME * 2
    private const val RATIO = DEVICE_RATE / GATEWAY_RATE

    fun downsample48kTo8k(input: ShortArray): ByteArray {
        require(input.size == DEVICE_SAMPLES_PER_FRAME)
        val output = ByteBuffer.allocate(GATEWAY_BYTES_PER_FRAME).order(ByteOrder.LITTLE_ENDIAN)
        for (sample in 0 until GATEWAY_SAMPLES_PER_FRAME) {
            var sum = 0
            val start = sample * RATIO
            for (offset in 0 until RATIO) sum += input[start + offset].toInt()
            output.putShort((sum / RATIO).toShort())
        }
        return output.array()
    }

    fun upsample8kTo48k(input: ByteArray): ShortArray {
        require(input.size == GATEWAY_BYTES_PER_FRAME)
        val source = ShortArray(GATEWAY_SAMPLES_PER_FRAME)
        ByteBuffer.wrap(input).order(ByteOrder.LITTLE_ENDIAN).asShortBuffer().get(source)
        val output = ShortArray(DEVICE_SAMPLES_PER_FRAME)
        for (sample in source.indices) {
            val current = source[sample].toInt()
            val next = source.getOrElse(sample + 1) { source[sample] }.toInt()
            for (phase in 0 until RATIO) {
                output[sample * RATIO + phase] =
                    (current + ((next - current) * phase / RATIO)).coerceIn(Short.MIN_VALUE.toInt(), Short.MAX_VALUE.toInt()).toShort()
            }
        }
        return output
    }
}

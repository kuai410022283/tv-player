package com.mediaplayer.app.util

import androidx.media3.common.C
import androidx.media3.common.audio.AudioProcessor
import androidx.media3.common.audio.BaseAudioProcessor
import java.nio.ByteBuffer

/**
 * 实时音频响度归一化与防爆音限幅处理器。
 * 平滑拉高过小音量频道，压低过大音量频道，并毫秒级限幅防止音响爆音。
 */
class AudioLoudnessProcessor : BaseAudioProcessor() {

    private var targetRms: Float = 0.15f // 目标 RMS 响度 (-16.5 dBFS)
    private var currentGain: Float = 1.0f

    override fun onConfigure(inputAudioFormat: AudioProcessor.AudioFormat): AudioProcessor.AudioFormat {
        if (inputAudioFormat.encoding != C.ENCODING_PCM_16BIT && inputAudioFormat.encoding != C.ENCODING_PCM_FLOAT) {
            return AudioProcessor.AudioFormat.NOT_SET
        }
        return inputAudioFormat
    }

    override fun queueInput(inputBuffer: ByteBuffer) {
        if (!inputBuffer.hasRemaining()) return

        val remaining = inputBuffer.remaining()
        val outputBuffer = replaceOutputBuffer(remaining)

        if (inputAudioFormat.encoding == C.ENCODING_PCM_16BIT) {
            processPcm16(inputBuffer, outputBuffer)
        } else if (inputAudioFormat.encoding == C.ENCODING_PCM_FLOAT) {
            processPcmFloat(inputBuffer, outputBuffer)
        } else {
            outputBuffer.put(inputBuffer)
        }

        outputBuffer.flip()
    }

    private fun processPcm16(input: ByteBuffer, output: ByteBuffer) {
        val sampleCount = input.remaining() / 2
        if (sampleCount <= 0) return

        val startPos = input.position()
        var sumSquare = 0.0

        // 1. 快速计算当前 Buffer 的 RMS 响度
        while (input.hasRemaining()) {
            val sample = input.short.toFloat() / 32768.0f
            sumSquare += (sample * sample).toDouble()
        }
        input.position(startPos)

        val rms = Math.sqrt(sumSquare / sampleCount).toFloat()

        // 2. 平滑增益调整 (Alpha 滤波，避免增益突变导致跳音)
        if (rms > 0.001f) {
            val desiredGain = (targetRms / rms).coerceIn(0.5f, 2.5f)
            currentGain = currentGain * 0.95f + desiredGain * 0.05f
        }

        // 3. 应用增益并进行软限幅 (Soft Limiter 防爆音)
        while (input.hasRemaining()) {
            var sample = (input.short.toFloat() / 32768.0f) * currentGain

            // Soft Limiter (动态压限)
            sample = when {
                sample > 0.95f -> 0.95f + (sample - 0.95f) * 0.1f
                sample < -0.95f -> -0.95f + (sample + 0.95f) * 0.1f
                else -> sample
            }.coerceIn(-1.0f, 1.0f)

            val pcm16 = (sample * 32767.0f).toInt().coerceIn(-32768, 32767).toShort()
            output.putShort(pcm16)
        }
    }

    private fun processPcmFloat(input: ByteBuffer, output: ByteBuffer) {
        val sampleCount = input.remaining() / 4
        if (sampleCount <= 0) return

        val startPos = input.position()
        var sumSquare = 0.0

        while (input.hasRemaining()) {
            val sample = input.float
            sumSquare += (sample * sample).toDouble()
        }
        input.position(startPos)

        val rms = Math.sqrt(sumSquare / sampleCount).toFloat()

        if (rms > 0.001f) {
            val desiredGain = (targetRms / rms).coerceIn(0.5f, 2.5f)
            currentGain = currentGain * 0.95f + desiredGain * 0.05f
        }

        while (input.hasRemaining()) {
            var sample = input.float * currentGain
            sample = when {
                sample > 0.95f -> 0.95f + (sample - 0.95f) * 0.1f
                sample < -0.95f -> -0.95f + (sample + 0.95f) * 0.1f
                else -> sample
            }.coerceIn(-1.0f, 1.0f)
            output.putFloat(sample)
        }
    }

    override fun onReset() {
        currentGain = 1.0f
    }
}

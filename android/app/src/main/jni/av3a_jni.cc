/*
 * Dedicated AV3A JNI bridge. This file intentionally has no FFmpeg includes or calls.
 */
#include <jni.h>
#include <android/log.h>
#include <dlfcn.h>

#include <cstdint>
#include <new>

extern "C" {
#include "avs3_decoder_interface.h"
}

#define LOG_TAG "Av3aJNI"
#define LOGE(...) __android_log_print(ANDROID_LOG_ERROR, LOG_TAG, __VA_ARGS__)
#define LOGI(...) __android_log_print(ANDROID_LOG_INFO, LOG_TAG, __VA_ARGS__)

namespace {

constexpr int kInvalidData = -1;
constexpr int kOtherError = -2;
constexpr int kMinimumOutputCapacity = 64 * 1024;
constexpr int kRenderBlockSize = 1024;
constexpr int kOutputChannels = 2;  // Binaural renderer always outputs stereo

// ── Binaural renderer API types (loaded via dlopen) ──

// Avs3MetaData stub — the actual struct is defined in the AVS3 decoder SDK headers.
// We allocate enough space for a zero-initialized copy. The renderer library was
// compiled with the real definition and reads from this pointer.
struct Avs3MetaData {
  char data[2048];
};

typedef void* (*PFCreateRenderer)(Avs3MetaData* metadata, int sampleRate, int blockSize);
typedef int   (*PFPutInterleavedAudioBuffer)(void* render, const float* buffer, int frameNum, int channelNum);
typedef int   (*PFGetBinauralInterleavedAudioBuffer)(void* render, float* buffer, int frameNum);
typedef int   (*PFDestroyRenderer)(void* render);

struct DecoderContext {
  AVS3DecoderHandle decoder = nullptr;
  bool first_frame = true;

  // Binaural renderer state (lazy-initialized on first decode)
  void*  render_handle = nullptr;
  void*  render        = nullptr;
  PFCreateRenderer                 CreateRenderer         = nullptr;
  PFPutInterleavedAudioBuffer      PutInterleavedAudioBuffer      = nullptr;
  PFGetBinauralInterleavedAudioBuffer GetBinauralInterleavedAudioBuffer = nullptr;
  PFDestroyRenderer                DestroyRenderer        = nullptr;

  // Float conversion buffer (heap-allocated, grows as needed)
  float* float_buffer      = nullptr;
  int    float_buffer_size = 0;

  // Output channel/sample-rate tracking — updated after each decode
  int output_channels    = 0;
  int output_sample_rate = 0;

  // Reusable raw PCM output buffer (heap-allocated in CreateDecoder)
  unsigned char* raw_pcm_buffer = nullptr;
  int            raw_pcm_buffer_size = 0;
};

// ── Binaural renderer helpers ──

bool InitBinauralRenderer(DecoderContext* context, int sample_rate) {
  context->render_handle = dlopen("libav3a_binaural_render.so", RTLD_LAZY);
  if (context->render_handle == nullptr) {
    LOGI("Binaural renderer not available: %s", dlerror());
    return false;
  }

  context->CreateRenderer    = (PFCreateRenderer)dlsym(context->render_handle, "CreateRenderer");
  context->PutInterleavedAudioBuffer = (PFPutInterleavedAudioBuffer)dlsym(context->render_handle, "PutInterleavedAudioBuffer");
  context->GetBinauralInterleavedAudioBuffer = (PFGetBinauralInterleavedAudioBuffer)dlsym(context->render_handle, "GetBinauralInterleavedAudioBuffer");
  context->DestroyRenderer   = (PFDestroyRenderer)dlsym(context->render_handle, "DestroyRenderer");

  if (context->CreateRenderer == nullptr ||
      context->PutInterleavedAudioBuffer == nullptr ||
      context->GetBinauralInterleavedAudioBuffer == nullptr ||
      context->DestroyRenderer == nullptr) {
    LOGE("Missing binaural renderer symbols");
    dlclose(context->render_handle);
    context->render_handle = nullptr;
    return false;
  }

  Avs3MetaData meta = {};
  context->render = context->CreateRenderer(&meta, sample_rate, kRenderBlockSize);
  if (context->render == nullptr) {
    LOGE("Failed to create binaural renderer instance");
    dlclose(context->render_handle);
    context->render_handle = nullptr;
    return false;
  }

  LOGI("Binaural renderer initialized (sampleRate=%d, blockSize=%d)", sample_rate, kRenderBlockSize);
  return true;
}

void DestroyBinauralRenderer(DecoderContext* context) {
  if (context->render != nullptr && context->DestroyRenderer != nullptr) {
    context->DestroyRenderer(context->render);
    context->render = nullptr;
  }
  if (context->render_handle != nullptr) {
    dlclose(context->render_handle);
    context->render_handle = nullptr;
  }
  delete[] context->float_buffer;
  context->float_buffer      = nullptr;
  context->float_buffer_size = 0;
  context->CreateRenderer    = nullptr;
  context->PutInterleavedAudioBuffer  = nullptr;
  context->GetBinauralInterleavedAudioBuffer = nullptr;
  context->DestroyRenderer   = nullptr;
}

/**
 * Render multi-channel PCM int16 to stereo binaural PCM int16 via the binaural renderer.
 * Returns the number of output bytes, or 0 if the renderer is not available / fails.
 *
 * The renderer is lazily initialized on first call (or re-initialized if sample rate changed).
 */
int RenderToStereo(DecoderContext* context,
                   int16_t* pcm_data, int num_frames, int channels, int sample_rate,
                   int16_t* output, int output_capacity_bytes) {
  // Lazy init: create renderer on first use or re-create if sample rate changed
  if (context->render == nullptr || sample_rate != context->output_sample_rate) {
    DestroyBinauralRenderer(context);
    if (!InitBinauralRenderer(context, sample_rate)) {
      return 0;
    }
  }

  // Ensure float conversion buffer is large enough
  int needed_float = num_frames * channels;
  if (context->float_buffer_size < needed_float) {
    delete[] context->float_buffer;
    context->float_buffer = new float[needed_float];
    context->float_buffer_size = needed_float;
  }

  // Convert int16 to float (range -1.0 .. 1.0)
  for (int i = 0; i < needed_float; i++) {
    context->float_buffer[i] = pcm_data[i] / 32768.0f;
  }

  // Feed all frames to the renderer
  if (context->PutInterleavedAudioBuffer(
          context->render, context->float_buffer, num_frames, channels) != 0) {
    LOGE("PutInterleavedAudioBuffer failed");
    return 0;
  }

  // Drain output in blocks of kRenderBlockSize stereo frames
  float out_block[kRenderBlockSize * kOutputChannels];
  int total_output_frames = 0;
  int max_output_frames = output_capacity_bytes / (kOutputChannels * 2);

  while (total_output_frames + kRenderBlockSize <= max_output_frames) {
    int ret = context->GetBinauralInterleavedAudioBuffer(
        context->render, out_block, kRenderBlockSize);
    if (ret != 0) {
      break;  // No more data available
    }

    int16_t* dst = output + total_output_frames * kOutputChannels;
    for (int i = 0; i < kRenderBlockSize * kOutputChannels; i++) {
      float tmp = out_block[i] * 32767.0f;
      if (tmp > 32767.0f) {
        tmp = 32767.0f;
      } else if (tmp < -32768.0f) {
        tmp = -32768.0f;
      }
      dst[i] = static_cast<int16_t>(tmp);
    }
    total_output_frames += kRenderBlockSize;
  }

  return total_output_frames * kOutputChannels * 2;
}

// ── Decoder lifecycle ──

bool CreateDecoder(DecoderContext* context) {
  context->decoder = avs3_create_decoder();
  context->first_frame = true;
  if (context->decoder == nullptr) {
    return false;
  }
  // Pre-allocate reusable raw PCM buffer to avoid new/delete per decode call
  context->raw_pcm_buffer = new (std::nothrow) unsigned char[kMinimumOutputCapacity];
  if (context->raw_pcm_buffer == nullptr) {
    avs3_destroy_decoder(context->decoder);
    context->decoder = nullptr;
    return false;
  }
  context->raw_pcm_buffer_size = kMinimumOutputCapacity;
  // NOTE: output_channels / output_sample_rate are 0 until first frame decoded.
  // Binaural renderer is lazily initialized in RenderToStereo() on first use.
  return true;
}

void DestroyDecoder(DecoderContext* context) {
  DestroyBinauralRenderer(context);
  delete[] context->raw_pcm_buffer;
  context->raw_pcm_buffer = nullptr;
  context->raw_pcm_buffer_size = 0;
  if (context->decoder != nullptr) {
    avs3_destroy_decoder(context->decoder);
    context->decoder = nullptr;
  }
}

/**
 * Lightweight reset — keeps the pre-allocated raw_pcm_buffer and float_buffer,
 * only destroys and recreates the AVS3 decoder handle and binaural renderer.
 * This avoids the new/delete overhead of DestroyDecoder+CreateDecoder.
 */
bool ResetDecoder(DecoderContext* context) {
  // Destroy binaural renderer (will be lazily recreated in RenderToStereo)
  DestroyBinauralRenderer(context);

  // Destroy and recreate only the AVS3 decoder handle
  if (context->decoder != nullptr) {
    avs3_destroy_decoder(context->decoder);
    context->decoder = nullptr;
  }
  context->decoder = avs3_create_decoder();
  context->first_frame = true;

  // Reset output tracking — will be updated by next decode
  context->output_channels = 0;
  context->output_sample_rate = 0;

  // raw_pcm_buffer is kept — already allocated and reusable
  return context->decoder != nullptr;
}

}  // namespace

// ── JNI entry points ──

extern "C" JNIEXPORT jstring JNICALL
Java_androidx_media3_decoder_av3a_Av3aLibrary_av3aGetVersion(JNIEnv* env, jclass) {
  return env->NewStringUTF("VividLib-10a5c714-direct");
}

extern "C" JNIEXPORT jlong JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aInit(JNIEnv*, jclass) {
  auto* context = new (std::nothrow) DecoderContext();
  if (context == nullptr || !CreateDecoder(context)) {
    delete context;
    return 0;
  }
  LOGI("Created direct AV3A decoder");
  return reinterpret_cast<jlong>(context);
}

extern "C" JNIEXPORT jboolean JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aReset(JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  if (context == nullptr) {
    return JNI_FALSE;
  }
  // Lightweight reset: keeps raw_pcm_buffer, only recreates decoder handle
  return ResetDecoder(context) ? JNI_TRUE : JNI_FALSE;
}

extern "C" JNIEXPORT jint JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aDecode(
    JNIEnv* env,
    jclass,
    jlong handle,
    jobject input_buffer,
    jint input_size,
    jobject output_buffer,
    jint output_size) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  auto* input = static_cast<unsigned char*>(env->GetDirectBufferAddress(input_buffer));
  auto* output = static_cast<unsigned char*>(env->GetDirectBufferAddress(output_buffer));
  if (context == nullptr || context->decoder == nullptr || input == nullptr || output == nullptr
      || input_size <= 0 || output_size < kMinimumOutputCapacity) {
    return kOtherError;
  }

  // ── Step 1: Decode AV3A frames to raw multi-channel PCM ──
  //
  // Use the pre-allocated heap buffer from DecoderContext to avoid
  // new/delete overhead per decode call.

  auto* raw_pcm = context->raw_pcm_buffer;
  int raw_pcm_offset = 0;
  int input_offset = 0;

  while (input_offset < input_size) {
    int header_consumed = 0;
    int result =
        parse_header(
            context->decoder,
            input + input_offset,
            input_size - input_offset,
            context->first_frame ? 1 : 0,
            &header_consumed,
            nullptr);

    if (result != AVS3_TRUE || header_consumed <= 0 ||
        header_consumed > (input_size - input_offset)) {
      break;
    }

    int output_bytes = 0;
    int payload_consumed = 0;
    result =
        avs3_decode(
            context->decoder,
            input + input_offset + header_consumed,
            input_size - input_offset - header_consumed,
            raw_pcm + raw_pcm_offset,
            &output_bytes,
            &payload_consumed);

    if (result != AVS3_TRUE || output_bytes <= 0 || payload_consumed <= 0) {
      break;
    }

    if (raw_pcm_offset + output_bytes > kMinimumOutputCapacity) {
      LOGE("Raw decoder output overflow");
      break;
    }

    raw_pcm_offset += output_bytes;
    input_offset += (header_consumed + payload_consumed);
    context->first_frame = false;
  }

  if (raw_pcm_offset == 0) {
    return kInvalidData;
  }

  // Get channel count and sample rate from the decoder.
  // NOTE: The decoder library may not set numChansOutput/outputFs reliably.
  // We use 0 to signal "keep Java's original values".
  int dec_channels  = context->decoder->numChansOutput;
  int dec_samplerate = context->decoder->outputFs;

  int num_frames = 0;
  if (dec_channels > 0) {
    num_frames = raw_pcm_offset / (dec_channels * 2);
  }
  int16_t* raw_samples = reinterpret_cast<int16_t*>(raw_pcm);

  int rendered_bytes = 0;

  // ── Step 2: Render multi-channel PCM to stereo via binaural renderer ──
  // (lazy init: renderer is created on first use with the correct sample rate)

  if (dec_channels > 0 && dec_samplerate > 0) {
    rendered_bytes = RenderToStereo(
        context,
        raw_samples, num_frames, dec_channels, dec_samplerate,
        reinterpret_cast<int16_t*>(output), output_size);
  }

  if (rendered_bytes > 0) {
    // After binaural rendering, output is always stereo
    context->output_channels = kOutputChannels;
    context->output_sample_rate = dec_samplerate;
  } else {
    // ── Step 3: Fallback — renderer unavailable, copy raw PCM with soft clamp ──
    // Keep output_channels=0 to signal "use Java's original values"
    context->output_channels = 0;
    context->output_sample_rate = 0;

    int max_copy = (raw_pcm_offset <= output_size) ? raw_pcm_offset : output_size;
    int16_t* dst = reinterpret_cast<int16_t*>(output);
    for (int i = 0; i < max_copy / 2; i++) {
      int32_t s = raw_samples[i];
      if (s > 32767) s = 32767;
      else if (s < -32767) s = -32767;
      dst[i] = static_cast<int16_t>(s);
    }
    rendered_bytes = max_copy;
  }

  return rendered_bytes;
}

extern "C" JNIEXPORT jint JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aGetChannelCount(
    JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  if (context == nullptr) return 0;
  return context->output_channels;
}

extern "C" JNIEXPORT jint JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aGetSampleRate(
    JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  if (context == nullptr) return 0;
  return context->output_sample_rate;
}

extern "C" JNIEXPORT void JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aRelease(
    JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  if (context == nullptr) {
    return;
  }
  DestroyDecoder(context);
  delete context;
}
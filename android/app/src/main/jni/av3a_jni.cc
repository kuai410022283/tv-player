/*
 * Dedicated AV3A JNI bridge. This file intentionally has no FFmpeg includes or calls.
 */
#include <jni.h>
#include <android/log.h>

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

struct DecoderContext {
  AVS3DecoderHandle decoder = nullptr;
  bool first_frame = true;
};

bool CreateDecoder(DecoderContext* context) {
  context->decoder = avs3_create_decoder();
  context->first_frame = true;
  return context->decoder != nullptr;
}

void DestroyDecoder(DecoderContext* context) {
  if (context->decoder != nullptr) {
    avs3_destroy_decoder(context->decoder);
    context->decoder = nullptr;
  }
}

}  // namespace

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
  DestroyDecoder(context);
  return CreateDecoder(context) ? JNI_TRUE : JNI_FALSE;
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

  int total_output_bytes = 0;
  int current_input_offset = 0;

  while (current_input_offset < input_size) {
    int header_consumed = 0;
    int result =
        parse_header(
            context->decoder,
            input + current_input_offset,
            input_size - current_input_offset,
            context->first_frame ? 1 : 0,
            &header_consumed,
            nullptr);
            
    if (result != AVS3_TRUE || header_consumed <= 0 || header_consumed > (input_size - current_input_offset)) {
      break; // stop decoding this buffer, likely end of data
    }

    int output_bytes = 0;
    int payload_consumed = 0;
    result =
        avs3_decode(
            context->decoder,
            input + current_input_offset + header_consumed,
            input_size - current_input_offset - header_consumed,
            output + total_output_bytes,
            &output_bytes,
            &payload_consumed);
            
    if (result != AVS3_TRUE || output_bytes <= 0 || payload_consumed <= 0) {
      break;
    }

    if (total_output_bytes + output_bytes > output_size) {
      LOGE("Decoder output overflow: %d > %d", total_output_bytes + output_bytes, output_size);
      break;
    }

    total_output_bytes += output_bytes;
    current_input_offset += (header_consumed + payload_consumed);
    context->first_frame = false;
  }

  if (total_output_bytes == 0) {
    return kInvalidData;
  }
  return total_output_bytes;
}

extern "C" JNIEXPORT jint JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aGetChannelCount(
    JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  return context != nullptr && context->decoder != nullptr
      ? context->decoder->numChansOutput
      : 0;
}

extern "C" JNIEXPORT jint JNICALL
Java_androidx_media3_decoder_av3a_Av3aDecoder_av3aGetSampleRate(
    JNIEnv*, jclass, jlong handle) {
  auto* context = reinterpret_cast<DecoderContext*>(handle);
  return context != nullptr && context->decoder != nullptr ? context->decoder->outputFs : 0;
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

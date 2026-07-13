#ifndef AVS3_DECODER_INTERFACE_H
#define AVS3_DECODER_INTERFACE_H

#ifdef __cplusplus
extern "C" {
#endif

// Status codes based on typical AVS3 library conventions
#define AVS3_TRUE 1
#define AVS3_FALSE 0
#define AVS3_DATA_NOT_ENOUGH -2

// Opaque context struct definition containing necessary fields mapped from JNI access
typedef struct AVS3Decoder {
    int numChansOutput;
    int outputFs;
    // Padding to ensure safe struct usage, though usually we only hold the pointer
    void* internal_data[64];
} AVS3Decoder;

typedef AVS3Decoder* AVS3DecoderHandle;

/**
 * Creates an AVS3 Decoder instance.
 * @return Handle to the decoder, or NULL on failure.
 */
AVS3DecoderHandle avs3_create_decoder(void);

/**
 * Destroys the AVS3 Decoder instance.
 * @param decoder Handle to the decoder.
 */
void avs3_destroy_decoder(AVS3DecoderHandle decoder);

/**
 * Parses the AVS3 frame header.
 * @param decoder Handle to the decoder.
 * @param input Pointer to the input buffer.
 * @param input_size Size of the input buffer.
 * @param is_first_frame 1 if first frame, 0 otherwise.
 * @param header_consumed Output pointer for the number of bytes consumed by the header.
 * @param unknown A pointer to an unknown structure (passed as nullptr in JNI).
 * @return AVS3_TRUE on success, error code otherwise.
 */
int parse_header(
    AVS3DecoderHandle decoder,
    unsigned char* input,
    int input_size,
    int is_first_frame,
    int* header_consumed,
    void* unknown
);

/**
 * Decodes the AVS3 payload.
 * @param decoder Handle to the decoder.
 * @param input Pointer to the input buffer payload (after header).
 * @param input_size Size of the input buffer payload.
 * @param output Pointer to the output PCM buffer.
 * @param output_bytes Output pointer for the number of decoded PCM bytes.
 * @param payload_consumed Output pointer for the number of bytes consumed from the payload.
 * @return AVS3_TRUE on success, error code otherwise.
 */
int avs3_decode(
    AVS3DecoderHandle decoder,
    unsigned char* input,
    int input_size,
    unsigned char* output,
    int* output_bytes,
    int* payload_consumed
);

#ifdef __cplusplus
}
#endif

#endif // AVS3_DECODER_INTERFACE_H

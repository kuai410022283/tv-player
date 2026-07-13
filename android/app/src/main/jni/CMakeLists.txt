cmake_minimum_required(VERSION 3.22.1)
project("av3aJNI")

add_library(${CMAKE_PROJECT_NAME} SHARED
            av3a_jni.cc)

# Define the prebuilt library
add_library(AVS3AudioDec SHARED IMPORTED)
set_target_properties(AVS3AudioDec PROPERTIES
    IMPORTED_LOCATION ${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/libAVS3AudioDec.so
)

find_library(log-lib log)

target_link_libraries(${CMAKE_PROJECT_NAME}
                      AVS3AudioDec
                      ${log-lib})

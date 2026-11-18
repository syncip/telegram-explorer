# WorkManager creates its Room database by reflection; R8 would otherwise
# drop the generated constructor and the app crashes on start.
-keep class * extends androidx.room.RoomDatabase { <init>(); }

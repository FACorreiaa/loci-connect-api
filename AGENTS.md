1. When searching for an itinerary, the LLM will redirect to /itinerary.
In that page, the user should be able to bookmark the individial endpoints that show up but also bookmark the full itinerary. 
The same should be done for /activities, /hotels and /restaurants:
Bookmark the whole list or add individual items to favorites. 

2. On my app the user, on the main page, can search things like:
"Itineraries in London"
"Hotels in London"
"Restaurants in London"
"Activities in London"

Itineraries should display the general information about the city, general points of interest and the itinerary based on user preferences. This is done. 
Hotels, Restaurants and Activities should display general information about the city and then a list of hotels, restaurants and activities respectivly. 

Im having issues on every search.
For itineraries: I get a 200
Request URL
http://localhost:8000/loci.chat.ChatService/StreamChat
Request Method
POST
Status Code
200 OK
Remote Address
127.0.0.1:8000
Referrer Policy
strict-origin-when-cross-origin

But it fails with an error:

On the client. 
connect-transport.ts:22 
 POST http://localhost:8000/loci.chat.ChatService/StreamChat net::ERR_INCOMPLETE_CHUNKED_ENCODING 200 (OK)
llm.ts:784 Error in Proto to SSE conversion: ConnectError: [unknown] network error
    at _ConnectError.from (chunk-VXQFPFDH.js?v=3b07aca7:86:14)
    at abort (chunk-VXQFPFDH.js?v=3b07aca7:3133:28)
Caused by: TypeError: network error

streaming-service.ts:105 Stream processing error: ConnectError: [unknown] network error
    at _ConnectError.from (chunk-VXQFPFDH.js?v=3b07aca7:86:14)
    at abort (chunk-VXQFPFDH.js?v=3b07aca7:3133:28)
Caused by: TypeError: network error
LoggedInDashboard.tsx:237 Streaming error: Stream error: ConnectError: [unknown] network error

on the server:

{"time":"2025-12-08T17:19:26.187716Z","level":"INFO","msg":"User preference profiles fetched successfully","method":"GetSearchProfiles","userID":"826f9e87-295c-416b-9641-18db0de738ff","count":1}
{"time":"2025-12-08T17:19:26.187761Z","level":"INFO","msg":"RPC completed","procedure":"/loci.profile.ProfileService/GetUserPreferenceProfiles","duration":"8.102666ms","duration_ms":8,"request_size_bytes":0,"response_size_bytes":133,"request_id":"17ecbf1b-62a3-40b7-9a70-3da5ab9766ee"}
{"time":"2025-12-08T17:19:26.192822Z","level":"INFO","msg":"User preference profiles fetched successfully","method":"GetSearchProfiles","userID":"826f9e87-295c-416b-9641-18db0de738ff","count":1}
{"time":"2025-12-08T17:19:26.192921Z","level":"INFO","msg":"RPC completed","procedure":"/loci.profile.ProfileService/GetUserPreferenceProfiles","duration":"6.693375ms","duration_ms":6,"request_size_bytes":0,"response_size_bytes":133,"request_id":"2520fa5b-ba7a-403f-8c7f-322a8754da80"}
{"time":"2025-12-08T17:19:36.242826Z","level":"INFO","msg":"Cache hit for LLM response","part_type":"city_data","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_city_data"}
{"time":"2025-12-08T17:19:36.242826Z","level":"INFO","msg":"Cache hit for LLM response","part_type":"general_pois","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_general_pois"}
{"time":"2025-12-08T17:19:36.242846Z","level":"INFO","msg":"Cache hit for LLM response","part_type":"hotels","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_hotels"}
{"time":"2025-12-08T17:19:36.242972Z","level":"INFO","msg":"Cache hit for LLM response","part_type":"itinerary","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_itinerary"}
{"time":"2025-12-08T17:19:36.242854Z","level":"INFO","msg":"Cache hit for LLM response","part_type":"activities","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_activities"}
{"time":"2025-12-08T17:19:36.2429Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"restaurants","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_restaurants"}
{"time":"2025-12-08T17:19:36.24305Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"restaurants","cache_key":"1a7e9d31e2c519ae8894d2c83c4fcfda_restaurants","prompt_length":1128}
2025/12/08 17:19:36 INFO Cache key provided but currently ignored in direct implementation cacheKey=1a7e9d31e2c519ae8894d2c83c4fcfda_restaurants
{"time":"2025-12-08T17:19:36.626467Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:19:36.929253Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":2,"chunk_length":30,"chunk_preview":" coordinates 0.0000, 0.0000 in"}
{"time":"2025-12-08T17:19:37.231845Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":3,"chunk_length":218,"chunk_preview":" London place you exactly at **Charing Cross/Trafalgar Square**, a very central and busy location. G"}
{"time":"2025-12-08T17:19:50.228949Z","level":"WARN","msg":"RPC context canceled","error":"context canceled","reason":"client_disconnected_or_timeout"}
{"time":"2025-12-08T17:19:50.256103Z","level":"WARN","msg":"Context canceled during streaming","part_type":"restaurants","chunks_received":45}
{"time":"2025-12-08T17:19:50.340584Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:19:50.341258Z","level":"WARN","msg":"failed to unmarshal raw","key":"general_pois","err":"unexpected end of JSON input"}
{"time":"2025-12-08T17:19:50.342478Z","level":"WARN","msg":"failed to unmarshal raw","key":"restaurants","err":"unexpected end of JSON input"}
{"time":"2025-12-08T17:19:50.342573Z","level":"WARN","msg":"failed to unmarshal raw","key":"activities","err":"unexpected end of JSON input"}
{"time":"2025-12-08T17:19:50.342603Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":9}
{"time":"2025-12-08T17:19:50.342736Z","level":"INFO","msg":"City not found in database, creating minimal entry","city_name":"London"}
{"time":"2025-12-08T17:19:50.342754Z","level":"WARN","msg":"Failed to create city entry","city":"London","error":"failed to insert city: context canceled"}
{"time":"2025-12-08T17:19:50.342963Z","level":"WARN","msg":"Failed to save interaction","error":"failed to start transaction: context canceled"}
{"time":"2025-12-08T17:19:50.342979Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"error"}
{"time":"2025-12-08T17:19:50.343025Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"6d82b107-0db3-474b-af96-373e0151ffec","type":"error","error":"failed to start transaction: context canceled"}

For restaurants:

1{"message":"Restaurants in London","cityName":""}
{"time":"2025-12-08T17:19:50.342754Z","level":"WARN","msg":"Failed to create city entry","city":"London","error":"failed to insert city: context canceled"}
{"time":"2025-12-08T17:19:50.342963Z","level":"WARN","msg":"Failed to save interaction","error":"failed to start transaction: context canceled"}
{"time":"2025-12-08T17:19:50.342979Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"error"}
{"time":"2025-12-08T17:19:50.343025Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"6d82b107-0db3-474b-af96-373e0151ffec","type":"error","error":"failed to start transaction: context canceled"}
{"time":"2025-12-08T17:21:21.460897Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"restaurants","cache_key":"0dd2c85d8a097054dc92f15a78552ce9_restaurants"}
{"time":"2025-12-08T17:21:21.461004Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"restaurants","cache_key":"0dd2c85d8a097054dc92f15a78552ce9_restaurants","prompt_length":1128}
2025/12/08 17:21:21 INFO Cache key provided but currently ignored in direct implementation cacheKey=0dd2c85d8a097054dc92f15a78552ce9_restaurants
{"time":"2025-12-08T17:21:21.738276Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:21:22.040909Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":2,"chunk_length":33,"chunk_preview":" coordinates 0.0000, 0.0000 place"}
{"time":"2025-12-08T17:21:22.342805Z","level":"INFO","msg":"Received chunk from LLM","part_type":"restaurants","chunk_number":3,"chunk_length":236,"chunk_preview":" us exactly at **Charing Cross, London**, which is a highly central location. This area is rich with"}
{"time":"2025-12-08T17:21:35.165941Z","level":"WARN","msg":"RPC context canceled","error":"context canceled","reason":"client_disconnected_or_timeout"}
{"time":"2025-12-08T17:21:35.266963Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"chunk"}
{"time":"2025-12-08T17:21:35.267095Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"7a9123ce-49f8-4e1d-af9c-3b99742d3337","type":"chunk","error":""}
{"time":"2025-12-08T17:21:35.268669Z","level":"WARN","msg":"Context canceled during streaming","part_type":"restaurants","chunks_received":45}
{"time":"2025-12-08T17:21:35.268846Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:21:35.268985Z","level":"WARN","msg":"failed to unmarshal raw","key":"restaurants","err":"unexpected end of JSON input"}
{"time":"2025-12-08T17:21:35.268996Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":0}
{"time":"2025-12-08T17:21:35.269053Z","level":"INFO","msg":"City not found in database, creating minimal entry","city_name":"London"}
{"time":"2025-12-08T17:21:35.269081Z","level":"WARN","msg":"Failed to create city entry","city":"London","error":"failed to insert city: context canceled"}
{"time":"2025-12-08T17:21:35.269399Z","level":"WARN","msg":"Failed to save interaction","error":"failed to start transaction: context canceled"}
{"time":"2025-12-08T17:21:35.269422Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"error"}
{"time":"2025-12-08T17:21:35.26947Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"f55a28f1-cf0b-419a-a66e-c473e37fd6d0","type":"error","error":"failed to start transaction: context canceled"}


For Hotels:

In Hotels I get a resposnse:
   1{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2YyIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwic2Vzc2lvbl9pZCI6IjhmNjU1YjVhLTllZDQtNGFmOS1iYjkxLTRjZmUzMzZhODk3MSJ9",
    "timestamp": "2025-12-08T17:22:11.614244Z",
    "eventId": "e5cb9694-9cb9-4f71-a96c-e4a2e4f2e306"
}   1{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2YyIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwic2Vzc2lvbl9pZCI6IjhmNjU1YjVhLTllZDQtNGFmOS1iYjkxLTRjZmUzMzZhODk3MSJ9",
    "timestamp": "2025-12-08T17:22:11.614244Z",
    "eventId": "e5cb9694-9cb9-4f71-a96c-e4a2e4f2e306"
}   1{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2YyIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwic2Vzc2lvbl9pZCI6IjhmNjU1YjVhLTllZDQtNGFmOS1iYjkxLTRjZmUzMzZhODk3MSJ9",
    "timestamp": "2025-12-08T17:22:11.614244Z",
    "eventId": "e5cb9694-9cb9-4f71-a96c-e4a2e4f2e306"
}   !{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJUaGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:12.536287Z",
    "eventId": "e53c2f30-f715-41ad-b5b5-08f81f0343c3"
}   !{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJUaGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:12.536287Z",
    "eventId": "e53c2f30-f715-41ad-b5b5-08f81f0343c3"
}   !{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJUaGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:12.536287Z",
    "eventId": "e53c2f30-f715-41ad-b5b5-08f81f0343c3"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgKDAuMDAwMCwgMC4wMDAwKSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:12.838968Z",
    "eventId": "481f904f-3e90-4cb9-b04e-887a575dd6eb"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgKDAuMDAwMCwgMC4wMDAwKSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:12.838968Z",
    "eventId": "481f904f-3e90-4cb9-b04e-887a575dd6eb"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgKDAuMDAwMCwgMC4wMDAwKSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:12.838968Z",
    "eventId": "481f904f-3e90-4cb9-b04e-887a575dd6eb"
}   
]{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29ycmVzcG9uZCB0byB0aGUgaW50ZXJzZWN0aW9uIG9mIHRoZSBQcmltZSBNZXJpZGlhbiAoMMKwIGxvbmdpdHVkZSkgYW5kIHRoZSBFcXVhdG9yICgwwrAgbGF0aXR1ZGUpLCB3aGljaCBpcyBpbiB0aGUgQXRsYW50aWMgT2NlYW4sIG9mZiB0aGUgY29hc3Qgb2YgV2VzdCBBZnJpY2EuXG5cbkhvd2V2ZXIsIHNpbmNlIHRoZSB1c2VyIGV4cGxpY2l0bHkgcmVxdWVzdGVkIGFjY29tbW9kYXRpb24gaW4gKipMb25kb24qKiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.142339Z",
    "eventId": "ad66c299-ee32-43f5-be8d-960702749a83"
}   
]{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29ycmVzcG9uZCB0byB0aGUgaW50ZXJzZWN0aW9uIG9mIHRoZSBQcmltZSBNZXJpZGlhbiAoMMKwIGxvbmdpdHVkZSkgYW5kIHRoZSBFcXVhdG9yICgwwrAgbGF0aXR1ZGUpLCB3aGljaCBpcyBpbiB0aGUgQXRsYW50aWMgT2NlYW4sIG9mZiB0aGUgY29hc3Qgb2YgV2VzdCBBZnJpY2EuXG5cbkhvd2V2ZXIsIHNpbmNlIHRoZSB1c2VyIGV4cGxpY2l0bHkgcmVxdWVzdGVkIGFjY29tbW9kYXRpb24gaW4gKipMb25kb24qKiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.142339Z",
    "eventId": "ad66c299-ee32-43f5-be8d-960702749a83"
}   
]{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29ycmVzcG9uZCB0byB0aGUgaW50ZXJzZWN0aW9uIG9mIHRoZSBQcmltZSBNZXJpZGlhbiAoMMKwIGxvbmdpdHVkZSkgYW5kIHRoZSBFcXVhdG9yICgwwrAgbGF0aXR1ZGUpLCB3aGljaCBpcyBpbiB0aGUgQXRsYW50aWMgT2NlYW4sIG9mZiB0aGUgY29hc3Qgb2YgV2VzdCBBZnJpY2EuXG5cbkhvd2V2ZXIsIHNpbmNlIHRoZSB1c2VyIGV4cGxpY2l0bHkgcmVxdWVzdGVkIGFjY29tbW9kYXRpb24gaW4gKipMb25kb24qKiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.142339Z",
    "eventId": "ad66c299-ee32-43f5-be8d-960702749a83"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgSSB3aWxsIGFzc3VtZSB0aGUgdXNlciBpbnRlbmRlZCB0byBzZWFyY2ggbmVhciB0aGUgZ2VvZ3JhcGhpYyBjZW50ZXIgb2YgTG9uZG9uLCBvciBwZXJoYXBzIG5lYXIgYSBzaWduaWZpY2FudCBsYW5kbWFyayBpbiBMb25kb24gdGhhdCBpcyBvZnRlbiBhc3NvY2lhdGVkIHdpdGggdGhlIFByaW1lIE1lcmlkaWFuJ3MgaGlzdG9yaWNhbCBjb250ZXh0IChsaWtlIEdyZWVud2ljaCwgYWx0aG91Z2ggMCwwIGlzIG5vdCBHcmVlbndpY2gpLiIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:13.445864Z",
    "eventId": "58a2ec83-898a-4947-a05a-2cdeeda9724b"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgSSB3aWxsIGFzc3VtZSB0aGUgdXNlciBpbnRlbmRlZCB0byBzZWFyY2ggbmVhciB0aGUgZ2VvZ3JhcGhpYyBjZW50ZXIgb2YgTG9uZG9uLCBvciBwZXJoYXBzIG5lYXIgYSBzaWduaWZpY2FudCBsYW5kbWFyayBpbiBMb25kb24gdGhhdCBpcyBvZnRlbiBhc3NvY2lhdGVkIHdpdGggdGhlIFByaW1lIE1lcmlkaWFuJ3MgaGlzdG9yaWNhbCBjb250ZXh0IChsaWtlIEdyZWVud2ljaCwgYWx0aG91Z2ggMCwwIGlzIG5vdCBHcmVlbndpY2gpLiIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:13.445864Z",
    "eventId": "58a2ec83-898a-4947-a05a-2cdeeda9724b"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgSSB3aWxsIGFzc3VtZSB0aGUgdXNlciBpbnRlbmRlZCB0byBzZWFyY2ggbmVhciB0aGUgZ2VvZ3JhcGhpYyBjZW50ZXIgb2YgTG9uZG9uLCBvciBwZXJoYXBzIG5lYXIgYSBzaWduaWZpY2FudCBsYW5kbWFyayBpbiBMb25kb24gdGhhdCBpcyBvZnRlbiBhc3NvY2lhdGVkIHdpdGggdGhlIFByaW1lIE1lcmlkaWFuJ3MgaGlzdG9yaWNhbCBjb250ZXh0IChsaWtlIEdyZWVud2ljaCwgYWx0aG91Z2ggMCwwIGlzIG5vdCBHcmVlbndpY2gpLiIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:13.445864Z",
    "eventId": "58a2ec83-898a-4947-a05a-2cdeeda9724b"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcblxuR2l2ZW4gdGhlIHN0cmljdCByZXF1aXJlbWVudCB0byBzZWFyY2ggbmVhciAqKigwLjAwMDAsIDAuMDAwMCkqKiwgYW5kIHRoZSBzZWFyY2ggcmFkaXVzIG9mICoqNS4wIGttKiosIG5vIGhvdGVscyBpbiBMb25kb24gd2lsbCBiZSBmb3VuZCwgYXMgTG9uZG9uIGlzIGFwcHJveGltYXRlbHkiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.748712Z",
    "eventId": "cb6e252e-e66f-4789-b0eb-2ebdd5213a1c"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcblxuR2l2ZW4gdGhlIHN0cmljdCByZXF1aXJlbWVudCB0byBzZWFyY2ggbmVhciAqKigwLjAwMDAsIDAuMDAwMCkqKiwgYW5kIHRoZSBzZWFyY2ggcmFkaXVzIG9mICoqNS4wIGttKiosIG5vIGhvdGVscyBpbiBMb25kb24gd2lsbCBiZSBmb3VuZCwgYXMgTG9uZG9uIGlzIGFwcHJveGltYXRlbHkiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.748712Z",
    "eventId": "cb6e252e-e66f-4789-b0eb-2ebdd5213a1c"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcblxuR2l2ZW4gdGhlIHN0cmljdCByZXF1aXJlbWVudCB0byBzZWFyY2ggbmVhciAqKigwLjAwMDAsIDAuMDAwMCkqKiwgYW5kIHRoZSBzZWFyY2ggcmFkaXVzIG9mICoqNS4wIGttKiosIG5vIGhvdGVscyBpbiBMb25kb24gd2lsbCBiZSBmb3VuZCwgYXMgTG9uZG9uIGlzIGFwcHJveGltYXRlbHkiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:13.748712Z",
    "eventId": "cb6e252e-e66f-4789-b0eb-2ebdd5213a1c"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNSwxNTAga20gYXdheSBmcm9tICgwLjAwMDAsIDAuMDAwMCkuXG5cblRvIHByb3ZpZGUgYSBoZWxwZnVsIHJlc3BvbnNlIHRoYXQgYWRoZXJlcyB0byB0aGUgc3Bpcml0IG9mIGZpbmRpbmcgTG9uZG9uIGFjY29tbW9kYXRpb24gd2hpbGUgYWNrbm93bGVkZ2luZyB0aGUgaW1wb3NzaWJsZSBjb29yZGluYXRlcywgSSB3aWxsIHNlYXJjaCIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.050137Z",
    "eventId": "b40024dd-b5f4-4abe-b436-c2314f3d0fa3"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNSwxNTAga20gYXdheSBmcm9tICgwLjAwMDAsIDAuMDAwMCkuXG5cblRvIHByb3ZpZGUgYSBoZWxwZnVsIHJlc3BvbnNlIHRoYXQgYWRoZXJlcyB0byB0aGUgc3Bpcml0IG9mIGZpbmRpbmcgTG9uZG9uIGFjY29tbW9kYXRpb24gd2hpbGUgYWNrbm93bGVkZ2luZyB0aGUgaW1wb3NzaWJsZSBjb29yZGluYXRlcywgSSB3aWxsIHNlYXJjaCIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.050137Z",
    "eventId": "b40024dd-b5f4-4abe-b436-c2314f3d0fa3"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNSwxNTAga20gYXdheSBmcm9tICgwLjAwMDAsIDAuMDAwMCkuXG5cblRvIHByb3ZpZGUgYSBoZWxwZnVsIHJlc3BvbnNlIHRoYXQgYWRoZXJlcyB0byB0aGUgc3Bpcml0IG9mIGZpbmRpbmcgTG9uZG9uIGFjY29tbW9kYXRpb24gd2hpbGUgYWNrbm93bGVkZ2luZyB0aGUgaW1wb3NzaWJsZSBjb29yZGluYXRlcywgSSB3aWxsIHNlYXJjaCIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.050137Z",
    "eventId": "b40024dd-b5f4-4abe-b436-c2314f3d0fa3"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgbmVhciB0aGUgKipHZW9ncmFwaGljIENlbnRlciBvZiBMb25kb24gKGFwcHJveC4gNTEuNTA3NMKwIE4sIDAuMTI3OMKwIFcpKiosIGFzIHRoaXMgaXMgdGhlIG1vc3QgbG9naWNhbCBpbnRlcnByZXRhdGlvbiB3aGVuIGEgdXNlciBzcGVjaWZpZXMgYSBjaXR5IGJ1dCBwcm92aWRlcyIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.353140Z",
    "eventId": "e0a0282a-b9b3-4cc0-b8a4-4217fe8dcf36"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgbmVhciB0aGUgKipHZW9ncmFwaGljIENlbnRlciBvZiBMb25kb24gKGFwcHJveC4gNTEuNTA3NMKwIE4sIDAuMTI3OMKwIFcpKiosIGFzIHRoaXMgaXMgdGhlIG1vc3QgbG9naWNhbCBpbnRlcnByZXRhdGlvbiB3aGVuIGEgdXNlciBzcGVjaWZpZXMgYSBjaXR5IGJ1dCBwcm92aWRlcyIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.353140Z",
    "eventId": "e0a0282a-b9b3-4cc0-b8a4-4217fe8dcf36"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgbmVhciB0aGUgKipHZW9ncmFwaGljIENlbnRlciBvZiBMb25kb24gKGFwcHJveC4gNTEuNTA3NMKwIE4sIDAuMTI3OMKwIFcpKiosIGFzIHRoaXMgaXMgdGhlIG1vc3QgbG9naWNhbCBpbnRlcnByZXRhdGlvbiB3aGVuIGEgdXNlciBzcGVjaWZpZXMgYSBjaXR5IGJ1dCBwcm92aWRlcyIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:14.353140Z",
    "eventId": "e0a0282a-b9b3-4cc0-b8a4-4217fe8dcf36"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgZmFyIG91dHNpZGUgaXQuXG5cbkkgd2lsbCB1c2UgY29vcmRpbmF0ZXMgbmVhciBUcmFmYWxnYXIgU3F1YXJlL0NoYXJpbmcgQ3Jvc3MgYXMgYSBjZW50cmFsIExvbmRvbiByZWZlcmVuY2UgcG9pbnQgKGFwcHJveC4gNTEuNTA4MCwgLTAuMTI4MCkgYW5kIHNlYXJjaCB3aXRoaW4gYSA1LjAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:14.655670Z",
    "eventId": "2af99540-4b7b-4ec5-934a-cd2f00d97e6b"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgZmFyIG91dHNpZGUgaXQuXG5cbkkgd2lsbCB1c2UgY29vcmRpbmF0ZXMgbmVhciBUcmFmYWxnYXIgU3F1YXJlL0NoYXJpbmcgQ3Jvc3MgYXMgYSBjZW50cmFsIExvbmRvbiByZWZlcmVuY2UgcG9pbnQgKGFwcHJveC4gNTEuNTA4MCwgLTAuMTI4MCkgYW5kIHNlYXJjaCB3aXRoaW4gYSA1LjAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:14.655670Z",
    "eventId": "2af99540-4b7b-4ec5-934a-cd2f00d97e6b"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgY29vcmRpbmF0ZXMgZmFyIG91dHNpZGUgaXQuXG5cbkkgd2lsbCB1c2UgY29vcmRpbmF0ZXMgbmVhciBUcmFmYWxnYXIgU3F1YXJlL0NoYXJpbmcgQ3Jvc3MgYXMgYSBjZW50cmFsIExvbmRvbiByZWZlcmVuY2UgcG9pbnQgKGFwcHJveC4gNTEuNTA4MCwgLTAuMTI4MCkgYW5kIHNlYXJjaCB3aXRoaW4gYSA1LjAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:14.655670Z",
    "eventId": "2af99540-4b7b-4ec5-934a-cd2f00d97e6b"
}   9{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIga20gcmFkaXVzLCBrZWVwaW5nIHRoZSBidWRnZXQgbGV2ZWwgKDAgLSBhbnkpIGFuZCBvdGhlciBwcmVmZXJlbmNlcyBpbiBtaW5kLlxuXG5IZXJlIGFyZSBzb21lIHN1aXRhYmxlLCBjZW50cmFsbHkgbG9jYXRlZCBvcHRpb25zIGluIExvbmRvbjpcblxuYGBganNvblxue1xuICAgIFwiaG90ZWxzXCI6IFtcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:14.959057Z",
    "eventId": "d8c5a8f1-285a-4e2f-a181-e77d649bbc0c"
}   9{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIga20gcmFkaXVzLCBrZWVwaW5nIHRoZSBidWRnZXQgbGV2ZWwgKDAgLSBhbnkpIGFuZCBvdGhlciBwcmVmZXJlbmNlcyBpbiBtaW5kLlxuXG5IZXJlIGFyZSBzb21lIHN1aXRhYmxlLCBjZW50cmFsbHkgbG9jYXRlZCBvcHRpb25zIGluIExvbmRvbjpcblxuYGBganNvblxue1xuICAgIFwiaG90ZWxzXCI6IFtcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:14.959057Z",
    "eventId": "d8c5a8f1-285a-4e2f-a181-e77d649bbc0c"
}   9{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIga20gcmFkaXVzLCBrZWVwaW5nIHRoZSBidWRnZXQgbGV2ZWwgKDAgLSBhbnkpIGFuZCBvdGhlciBwcmVmZXJlbmNlcyBpbiBtaW5kLlxuXG5IZXJlIGFyZSBzb21lIHN1aXRhYmxlLCBjZW50cmFsbHkgbG9jYXRlZCBvcHRpb25zIGluIExvbmRvbjpcblxuYGBganNvblxue1xuICAgIFwiaG90ZWxzXCI6IFtcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:14.959057Z",
    "eventId": "d8c5a8f1-285a-4e2f-a181-e77d649bbc0c"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSBUcmFmYWxnYXIgU3QuIEphbWVzXCIsXG4gICAgICAgICAgICBcImxhdGl0dWRlXCI6IDUxLjUwODAsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjcwLFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkhvdGVsXCIsXG4gICAgICAgICAgICAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.262063Z",
    "eventId": "3e1cd41c-9b30-4ecd-bfd3-2cbe0a35d9a8"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSBUcmFmYWxnYXIgU3QuIEphbWVzXCIsXG4gICAgICAgICAgICBcImxhdGl0dWRlXCI6IDUxLjUwODAsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjcwLFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkhvdGVsXCIsXG4gICAgICAgICAgICAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.262063Z",
    "eventId": "3e1cd41c-9b30-4ecd-bfd3-2cbe0a35d9a8"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSBUcmFmYWxnYXIgU3QuIEphbWVzXCIsXG4gICAgICAgICAgICBcImxhdGl0dWRlXCI6IDUxLjUwODAsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjcwLFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkhvdGVsXCIsXG4gICAgICAgICAgICAiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.262063Z",
    "eventId": "3e1cd41c-9b30-4ecd-bfd3-2cbe0a35d9a8"
}   
}{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcImRlc2NyaXB0aW9uXCI6IFwiQSBsdXh1cnkgaG90ZWwgbG9jYXRlZCByaWdodCBvbiBUcmFmYWxnYXIgU3F1YXJlLCBvZmZlcmluZyBleGNlbGxlbnQgY2VudHJhbCBhY2Nlc3MgdG8gTG9uZG9uIGF0dHJhY3Rpb25zLiBGaXRzIHRoZSAnYW55IGJ1ZGdldCcgY3JpdGVyaWEgYXMgd2UgYXJlIHNlYXJjaGluZyBicm9hZGx5LlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiMiBTcHJpbmcgR2FyZGVucywgVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFNXIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:15.565209Z",
    "eventId": "9496a4b3-56c9-4543-a16a-79b32e65e10f"
}   
}{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcImRlc2NyaXB0aW9uXCI6IFwiQSBsdXh1cnkgaG90ZWwgbG9jYXRlZCByaWdodCBvbiBUcmFmYWxnYXIgU3F1YXJlLCBvZmZlcmluZyBleGNlbGxlbnQgY2VudHJhbCBhY2Nlc3MgdG8gTG9uZG9uIGF0dHJhY3Rpb25zLiBGaXRzIHRoZSAnYW55IGJ1ZGdldCcgY3JpdGVyaWEgYXMgd2UgYXJlIHNlYXJjaGluZyBicm9hZGx5LlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiMiBTcHJpbmcgR2FyZGVucywgVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFNXIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:15.565209Z",
    "eventId": "9496a4b3-56c9-4543-a16a-79b32e65e10f"
}   
}{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcImRlc2NyaXB0aW9uXCI6IFwiQSBsdXh1cnkgaG90ZWwgbG9jYXRlZCByaWdodCBvbiBUcmFmYWxnYXIgU3F1YXJlLCBvZmZlcmluZyBleGNlbGxlbnQgY2VudHJhbCBhY2Nlc3MgdG8gTG9uZG9uIGF0dHJhY3Rpb25zLiBGaXRzIHRoZSAnYW55IGJ1ZGdldCcgY3JpdGVyaWEgYXMgd2UgYXJlIHNlYXJjaGluZyBicm9hZGx5LlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiMiBTcHJpbmcgR2FyZGVucywgVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFNXIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:15.565209Z",
    "eventId": "9496a4b3-56c9-4543-a16a-79b32e65e10f"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIxQSAyQlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgzOSAzNjAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS9cIiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.868590Z",
    "eventId": "4d4808b3-2e6a-4b7b-b9a7-d9baaebbecc6"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIxQSAyQlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgzOSAzNjAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS9cIiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.868590Z",
    "eventId": "4d4808b3-2e6a-4b7b-b9a7-d9baaebbecc6"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIxQSAyQlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgzOSAzNjAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS9cIiwiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:15.868590Z",
    "eventId": "4d4808b3-2e6a-4b7b-b9a7-d9baaebbecc6"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIjI0IGhvdXJzXCIsXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiJCQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkx1eHVyeVwiLFxuICAgICAgICAgICAgICAgIFwiTmVhciIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.171872Z",
    "eventId": "5a6e9459-f64b-4105-809f-89f30cec87c1"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIjI0IGhvdXJzXCIsXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiJCQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkx1eHVyeVwiLFxuICAgICAgICAgICAgICAgIFwiTmVhciIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.171872Z",
    "eventId": "5a6e9459-f64b-4105-809f-89f30cec87c1"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIjI0IGhvdXJzXCIsXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiJCQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkx1eHVyeVwiLFxuICAgICAgICAgICAgICAgIFwiTmVhciIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.171872Z",
    "eventId": "5a6e9459-f64b-4105-809f-89f30cec87c1"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgTGFuZG1hcmtzXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBudWxsLFxuICAgICAgICAgICAgXCJkaXN0YW5jZVwiOiAwLjE1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiU3RyYW5kIFBhbGFjZSBIb3RlbFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.473891Z",
    "eventId": "0b8a1381-70f7-422d-9a57-b159095d8120"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgTGFuZG1hcmtzXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBudWxsLFxuICAgICAgICAgICAgXCJkaXN0YW5jZVwiOiAwLjE1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiU3RyYW5kIFBhbGFjZSBIb3RlbFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.473891Z",
    "eventId": "0b8a1381-70f7-422d-9a57-b159095d8120"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgTGFuZG1hcmtzXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBudWxsLFxuICAgICAgICAgICAgXCJkaXN0YW5jZVwiOiAwLjE1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiU3RyYW5kIFBhbGFjZSBIb3RlbFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:16.473891Z",
    "eventId": "0b8a1381-70f7-422d-9a57-b159095d8120"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcIjogNTEuNTEyMCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNDUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:16.776955Z",
    "eventId": "6f486819-a659-4136-92c3-2d178a76d852"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcIjogNTEuNTEyMCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNDUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:16.776955Z",
    "eventId": "6f486819-a659-4136-92c3-2d178a76d852"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJcIjogNTEuNTEyMCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNDUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:16.776955Z",
    "eventId": "6f486819-a659-4136-92c3-2d178a76d852"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIjM3MiBTdHJhbmQsIExvbmRvbiBXQzJSIDBKSlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.079919Z",
    "eventId": "83a018f0-edc8-49a0-af0e-6173c63dd2b7"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIjM3MiBTdHJhbmQsIExvbmRvbiBXQzJSIDBKSlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.079919Z",
    "eventId": "83a018f0-edc8-49a0-af0e-6173c63dd2b7"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIjM3MiBTdHJhbmQsIExvbmRvbiBXQzJSIDBKSlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgNzgiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.079919Z",
    "eventId": "83a018f0-edc8-49a0-af0e-6173c63dd2b7"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzNiA4MDAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjoiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.383266Z",
    "eventId": "c8ade450-fffc-4bcd-92dd-da46e619b007"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzNiA4MDAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjoiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.383266Z",
    "eventId": "c8ade450-fffc-4bcd-92dd-da46e619b007"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzNiA4MDAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkJFwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjoiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:17.383266Z",
    "eventId": "c8ade450-fffc-4bcd-92dd-da46e619b007"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNC4yLFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkNvdmVudCBHYXJkZW5cIixcbiAgICAgICAgICAgICAgICBcIk1pZC1SYW5nZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC43IiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:17.686888Z",
    "eventId": "3d664c95-9da5-462a-b7c6-d9229bad783d"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNC4yLFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkNvdmVudCBHYXJkZW5cIixcbiAgICAgICAgICAgICAgICBcIk1pZC1SYW5nZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC43IiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:17.686888Z",
    "eventId": "3d664c95-9da5-462a-b7c6-d9229bad783d"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgNC4yLFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkNlbnRyYWxcIixcbiAgICAgICAgICAgICAgICBcIkNvdmVudCBHYXJkZW5cIixcbiAgICAgICAgICAgICAgICBcIk1pZC1SYW5nZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC43IiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:17.686888Z",
    "eventId": "3d664c95-9da5-462a-b7c6-d9229bad783d"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiI1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiVGhlIFogSG90ZWwgUGljY2FkaWxseVwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiA1MS41MTA1LFxuICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:17.988866Z",
    "eventId": "209d2126-6f4c-4059-8423-7735b97b048f"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiI1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiVGhlIFogSG90ZWwgUGljY2FkaWxseVwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiA1MS41MTA1LFxuICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:17.988866Z",
    "eventId": "209d2126-6f4c-4059-8423-7735b97b048f"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiI1XG4gICAgICAgIH0sXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLFxuICAgICAgICAgICAgXCJuYW1lXCI6IFwiVGhlIFogSG90ZWwgUGljY2FkaWxseVwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiA1MS41MTA1LFxuICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:17.988866Z",
    "eventId": "209d2126-6f4c-4059-8423-7735b97b048f"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzMTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCIyNSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:18.295444Z",
    "eventId": "1b5cb960-2b1c-4c0d-9e23-993c96b888f6"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzMTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCIyNSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:18.295444Z",
    "eventId": "1b5cb960-2b1c-4c0d-9e23-993c96b888f6"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIzMTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiSG90ZWxcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCIyNSIsImRvbWFpbiI6ImFjY29tbW9kYXRpb24iLCJwYXJ0IjoiaG90ZWxzIn0=",
    "timestamp": "2025-12-08T17:22:18.295444Z",
    "eventId": "1b5cb960-2b1c-4c0d-9e23-993c96b888f6"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgMzY0MCAwMzAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:18.598349Z",
    "eventId": "da00631f-7647-4422-8ea2-e613b239871c"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgMzY0MCAwMzAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:18.598349Z",
    "eventId": "da00631f-7647-4422-8ea2-e613b239871c"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiIgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWlwiLFxuICAgICAgICAgICAgXCJwaG9uZV9udW1iZXJcIjogXCIrNDQgMjAgMzY0MCAwMzAwXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy50aGUiLCJkb21haW4iOiJhY2NvbW1vZGF0aW9uIiwicGFydCI6ImhvdGVscyJ9",
    "timestamp": "2025-12-08T17:22:18.598349Z",
    "eventId": "da00631f-7647-4422-8ea2-e613b239871c"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJ6aG90ZWxzLmNvbS9waWNjYWRpbGx5XCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjAsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:18.900781Z",
    "eventId": "3e0eddf2-a1c5-4495-adc6-6c80c37dcd87"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJ6aG90ZWxzLmNvbS9waWNjYWRpbGx5XCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjAsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:18.900781Z",
    "eventId": "3e0eddf2-a1c5-4495-adc6-6c80c37dcd87"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJ6aG90ZWxzLmNvbS9waWNjYWRpbGx5XCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfaG91cnNcIjogXCIyNCBob3Vyc1wiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjAsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:18.900781Z",
    "eventId": "3e0eddf2-a1c5-4495-adc6-6c80c37dcd87"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJDb21wYWN0XCIsXG4gICAgICAgICAgICAgICAgXCJQaWNjYWRpbGx5XCIsXG4gICAgICAgICAgICAgICAgXCJWYWx1ZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC42MFxuICAgICAgICB9XG4gICAgXVxufVxuYGBgIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:19.202309Z",
    "eventId": "3a7dddbf-8da0-4685-873d-c31bc2e4f7db"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJDb21wYWN0XCIsXG4gICAgICAgICAgICAgICAgXCJQaWNjYWRpbGx5XCIsXG4gICAgICAgICAgICAgICAgXCJWYWx1ZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC42MFxuICAgICAgICB9XG4gICAgXVxufVxuYGBgIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:19.202309Z",
    "eventId": "3a7dddbf-8da0-4685-873d-c31bc2e4f7db"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhN2M1MmM5MGUzNjlhMmM2NDU3ZTliZDNlYzhiNDM2Y19ob3RlbHMiLCJjYWNoZV91c2VkIjpmYWxzZSwiY2h1bmsiOiJDb21wYWN0XCIsXG4gICAgICAgICAgICAgICAgXCJQaWNjYWRpbGx5XCIsXG4gICAgICAgICAgICAgICAgXCJWYWx1ZVwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogbnVsbCxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMC42MFxuICAgICAgICB9XG4gICAgXVxufVxuYGBgIiwiZG9tYWluIjoiYWNjb21tb2RhdGlvbiIsInBhcnQiOiJob3RlbHMifQ==",
    "timestamp": "2025-12-08T17:22:19.202309Z",
    "eventId": "3a7dddbf-8da0-4685-873d-c31bc2e4f7db"
}   #�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV0sIml0aW5lcmFyeV9yZXNwb25zZSI6eyJpdGluZXJhcnlfbmFtZSI6IiIsIm92ZXJhbGxfZGVzY3JpcHRpb24iOiIiLCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV19LCJob3RlbHMiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJUaGUgVHJhZmFsZ2FyIFN0LiBKYW1lcyIsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGx1eHVyeSBob3RlbCBsb2NhdGVkIHJpZ2h0IG9uIFRyYWZhbGdhciBTcXVhcmUsIG9mZmVyaW5nIGV4Y2VsbGVudCBjZW50cmFsIGFjY2VzcyB0byBMb25kb24gYXR0cmFjdGlvbnMuIEZpdHMgdGhlICdhbnkgYnVkZ2V0JyBjcml0ZXJpYSBhcyB3ZSBhcmUgc2VhcmNoaW5nIGJyb2FkbHkuIiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkJCQiLCJyYXRpbmciOjQuNiwidGFncyI6WyJDZW50cmFsIiwiTHV4dXJ5IiwiTmVhciBMYW5kbWFya3MiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwibGF0aXR1ZGUiOjUxLjUxMiwibG9uZ2l0dWRlIjotMC4xMjQ1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS4iLCJhZGRyZXNzIjoiMzcyIFN0cmFuZCwgTG9uZG9uIFdDMlIgMEpKIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzYgODAwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvIiwib3BlbmluZ19ob3VycyI6IjI0IGhvdXJzIiwicHJpY2VfcmFuZ2UiOiIkJCQiLCJyYXRpbmciOjQuMiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJpbWFnZXMiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwibmFtZSI6IlRoZSBaIEhvdGVsIFBpY2NhZGlsbHkiLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwiYWRkcmVzcyI6IjI1IFNoYWZ0ZXNidXJ5IEF2ZSwgTG9uZG9uIFcxRCA1RVoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgMzY0MCAwMzAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnRoZXpob3RlbHMuY29tL3BpY2NhZGlsbHkiLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkIiwicmF0aW5nIjo0LCJ0YWdzIjpbIkNvbXBhY3QiLCJQaWNjYWRpbGx5IiwiVmFsdWUiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifV0sInNlc3Npb25faWQiOiI4ZjY1NWI1YS05ZWQ0LTRhZjktYmI5MS00Y2ZlMzM2YTg5NzEifQ==",
    "timestamp": "2025-12-08T17:22:19.630518Z",
    "eventId": "9fbf795e-d73c-4d85-b9e2-544bad4ef037"
}   #�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV0sIml0aW5lcmFyeV9yZXNwb25zZSI6eyJpdGluZXJhcnlfbmFtZSI6IiIsIm92ZXJhbGxfZGVzY3JpcHRpb24iOiIiLCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV19LCJob3RlbHMiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJUaGUgVHJhZmFsZ2FyIFN0LiBKYW1lcyIsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGx1eHVyeSBob3RlbCBsb2NhdGVkIHJpZ2h0IG9uIFRyYWZhbGdhciBTcXVhcmUsIG9mZmVyaW5nIGV4Y2VsbGVudCBjZW50cmFsIGFjY2VzcyB0byBMb25kb24gYXR0cmFjdGlvbnMuIEZpdHMgdGhlICdhbnkgYnVkZ2V0JyBjcml0ZXJpYSBhcyB3ZSBhcmUgc2VhcmNoaW5nIGJyb2FkbHkuIiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkJCQiLCJyYXRpbmciOjQuNiwidGFncyI6WyJDZW50cmFsIiwiTHV4dXJ5IiwiTmVhciBMYW5kbWFya3MiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwibGF0aXR1ZGUiOjUxLjUxMiwibG9uZ2l0dWRlIjotMC4xMjQ1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS4iLCJhZGRyZXNzIjoiMzcyIFN0cmFuZCwgTG9uZG9uIFdDMlIgMEpKIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzYgODAwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvIiwib3BlbmluZ19ob3VycyI6IjI0IGhvdXJzIiwicHJpY2VfcmFuZ2UiOiIkJCQiLCJyYXRpbmciOjQuMiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJpbWFnZXMiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwibmFtZSI6IlRoZSBaIEhvdGVsIFBpY2NhZGlsbHkiLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwiYWRkcmVzcyI6IjI1IFNoYWZ0ZXNidXJ5IEF2ZSwgTG9uZG9uIFcxRCA1RVoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgMzY0MCAwMzAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnRoZXpob3RlbHMuY29tL3BpY2NhZGlsbHkiLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkIiwicmF0aW5nIjo0LCJ0YWdzIjpbIkNvbXBhY3QiLCJQaWNjYWRpbGx5IiwiVmFsdWUiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifV0sInNlc3Npb25faWQiOiI4ZjY1NWI1YS05ZWQ0LTRhZjktYmI5MS00Y2ZlMzM2YTg5NzEifQ==",
    "timestamp": "2025-12-08T17:22:19.630518Z",
    "eventId": "9fbf795e-d73c-4d85-b9e2-544bad4ef037"
}   #�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV0sIml0aW5lcmFyeV9yZXNwb25zZSI6eyJpdGluZXJhcnlfbmFtZSI6IiIsIm92ZXJhbGxfZGVzY3JpcHRpb24iOiIiLCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFRyYWZhbGdhciBTdC4gSmFtZXMiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiSG90ZWwiLCJkZXNjcmlwdGlvbiI6IkEgbHV4dXJ5IGhvdGVsIGxvY2F0ZWQgcmlnaHQgb24gVHJhZmFsZ2FyIFNxdWFyZSwgb2ZmZXJpbmcgZXhjZWxsZW50IGNlbnRyYWwgYWNjZXNzIHRvIExvbmRvbiBhdHRyYWN0aW9ucy4gRml0cyB0aGUgJ2FueSBidWRnZXQnIGNyaXRlcmlhIGFzIHdlIGFyZSBzZWFyY2hpbmcgYnJvYWRseS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ2VudHJhbCIsIkx1eHVyeSIsIk5lYXIgTGFuZG1hcmtzIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MTIsImxvbmdpdHVkZSI6LTAuMTI0NSwiY2F0ZWdvcnkiOiJIb3RlbCIsImRlc2NyaXB0aW9uIjoiQSBsYXJnZSwgd2VsbC1yZWdhcmRlZCBob3RlbCBpbiB0aGUgQ292ZW50IEdhcmRlbiBhcmVhLCBvZmZlcmluZyBlYXN5IGFjY2VzcyB0byB0aGVhdGVycyBhbmQgdHJhbnNwb3J0IGxpbmtzLiBTdWl0YWJsZSBmb3IgdGhlICdhbnknIHByZWZlcmVuY2UuIiwicmF0aW5nIjo0LjIsImFkZHJlc3MiOiIzNzIgU3RyYW5kLCBMb25kb24gV0MyUiAwSkoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgNzgzNiA4MDAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnN0cmFuZHBhbGFjZS5jby51ay8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiIyNCBob3VycyJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiNjY0MzNkMjktNTU0MS00ZjYwLTkxZGItMmZhM2NkZTI0M2M1IiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIFogSG90ZWwgUGljY2FkaWxseSIsImRpc3RhbmNlIjowLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwicmF0aW5nIjo0LCJhZGRyZXNzIjoiMjUgU2hhZnRlc2J1cnkgQXZlLCBMb25kb24gVzFEIDVFWiIsInBob25lX251bWJlciI6Iis0NCAyMCAzNjQwIDAzMDAiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cudGhlemhvdGVscy5jb20vcGljY2FkaWxseSIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IjI0IGhvdXJzIn0sInByaWNlX3JhbmdlIjoiJCQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjY2NDMzZDI5LTU1NDEtNGY2MC05MWRiLTJmYTNjZGUyNDNjNSIsInRhZ3MiOlsiQ29tcGFjdCIsIlBpY2NhZGlsbHkiLCJWYWx1ZSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifV19LCJob3RlbHMiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJUaGUgVHJhZmFsZ2FyIFN0LiBKYW1lcyIsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGx1eHVyeSBob3RlbCBsb2NhdGVkIHJpZ2h0IG9uIFRyYWZhbGdhciBTcXVhcmUsIG9mZmVyaW5nIGV4Y2VsbGVudCBjZW50cmFsIGFjY2VzcyB0byBMb25kb24gYXR0cmFjdGlvbnMuIEZpdHMgdGhlICdhbnkgYnVkZ2V0JyBjcml0ZXJpYSBhcyB3ZSBhcmUgc2VhcmNoaW5nIGJyb2FkbHkuIiwiYWRkcmVzcyI6IjIgU3ByaW5nIEdhcmRlbnMsIFRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBTVzFBIDJCIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzkgMzYwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy50aGV0cmFmYWxnYXJzdGphbWVzLmNvbS8iLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkJCQiLCJyYXRpbmciOjQuNiwidGFncyI6WyJDZW50cmFsIiwiTHV4dXJ5IiwiTmVhciBMYW5kbWFya3MiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsIm5hbWUiOiJTdHJhbmQgUGFsYWNlIEhvdGVsIiwibGF0aXR1ZGUiOjUxLjUxMiwibG9uZ2l0dWRlIjotMC4xMjQ1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGxhcmdlLCB3ZWxsLXJlZ2FyZGVkIGhvdGVsIGluIHRoZSBDb3ZlbnQgR2FyZGVuIGFyZWEsIG9mZmVyaW5nIGVhc3kgYWNjZXNzIHRvIHRoZWF0ZXJzIGFuZCB0cmFuc3BvcnQgbGlua3MuIFN1aXRhYmxlIGZvciB0aGUgJ2FueScgcHJlZmVyZW5jZS4iLCJhZGRyZXNzIjoiMzcyIFN0cmFuZCwgTG9uZG9uIFdDMlIgMEpKIiwicGhvbmVfbnVtYmVyIjoiKzQ0IDIwIDc4MzYgODAwMCIsIndlYnNpdGUiOiJodHRwczovL3d3dy5zdHJhbmRwYWxhY2UuY28udWsvIiwib3BlbmluZ19ob3VycyI6IjI0IGhvdXJzIiwicHJpY2VfcmFuZ2UiOiIkJCQiLCJyYXRpbmciOjQuMiwidGFncyI6WyJDZW50cmFsIiwiQ292ZW50IEdhcmRlbiIsIk1pZC1SYW5nZSJdLCJpbWFnZXMiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwibmFtZSI6IlRoZSBaIEhvdGVsIFBpY2NhZGlsbHkiLCJsYXRpdHVkZSI6NTEuNTEwNSwibG9uZ2l0dWRlIjotMC4xMzE1LCJjYXRlZ29yeSI6IkhvdGVsIiwiZGVzY3JpcHRpb24iOiJBIGNvbXBhY3QsIG1vZGVybiBob3RlbCBrbm93biBmb3IgaXRzIGV4Y2VsbGVudCBsb2NhdGlvbiBuZWFyIFBpY2NhZGlsbHkgQ2lyY3VzLCBwcm92aWRpbmcgZ29vZCB2YWx1ZSBpbiBhIHByaW1lIHNwb3QuIiwiYWRkcmVzcyI6IjI1IFNoYWZ0ZXNidXJ5IEF2ZSwgTG9uZG9uIFcxRCA1RVoiLCJwaG9uZV9udW1iZXIiOiIrNDQgMjAgMzY0MCAwMzAwIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LnRoZXpob3RlbHMuY29tL3BpY2NhZGlsbHkiLCJvcGVuaW5nX2hvdXJzIjoiMjQgaG91cnMiLCJwcmljZV9yYW5nZSI6IiQkIiwicmF0aW5nIjo0LCJ0YWdzIjpbIkNvbXBhY3QiLCJQaWNjYWRpbGx5IiwiVmFsdWUiXSwiaW1hZ2VzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAifV0sInNlc3Npb25faWQiOiI4ZjY1NWI1YS05ZWQ0LTRhZjktYmI5MS00Y2ZlMzM2YTg5NzEifQ==",
    "timestamp": "2025-12-08T17:22:19.630518Z",
    "eventId": "9fbf795e-d73c-4d85-b9e2-544bad4ef037"
}   �{
    "type": "complete",
    "data": "eyJzZXNzaW9uX2lkIjoiOGY2NTViNWEtOWVkNC00YWY5LWJiOTEtNGNmZTMzNmE4OTcxIn0=",
    "timestamp": "2025-12-08T17:22:19.951050Z",
    "eventId": "d712bf5e-aa9a-404f-ab2c-e19adc33c5eb",
    "navigation": {
        "url": "/hotels?sessionId=8f655b5a-9ed4-4af9-bb91-4cfe336a8971&cityName=London&domain=hotels",
        "routeType": "hotels",
        "queryParams": {
            "cityName": "London",
            "domain": "hotels",
            "sessionId": "8f655b5a-9ed4-4af9-bb91-4cfe336a8971"
        }
    }
}   {}

{"time":"2025-12-08T17:21:35.26947Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"f55a28f1-cf0b-419a-a66e-c473e37fd6d0","type":"error","error":"failed to start transaction: context canceled"}
{"time":"2025-12-08T17:22:11.917393Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"hotels","cache_key":"a7c52c90e369a2c6457e9bd3ec8b436c_hotels"}
{"time":"2025-12-08T17:22:11.91745Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"hotels","cache_key":"a7c52c90e369a2c6457e9bd3ec8b436c_hotels","prompt_length":1166}
2025/12/08 17:22:11 INFO Cache key provided but currently ignored in direct implementation cacheKey=a7c52c90e369a2c6457e9bd3ec8b436c_hotels
{"time":"2025-12-08T17:22:12.536232Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:22:12.838835Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":2,"chunk_length":29,"chunk_preview":" coordinates (0.0000, 0.0000)"}
{"time":"2025-12-08T17:22:13.142299Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":3,"chunk_length":238,"chunk_preview":" correspond to the intersection of the Prime Meridian (0° longitude) and the Equator (0° latitude)"}
{"time":"2025-12-08T17:22:19.505563Z","level":"INFO","msg":"LLM streaming completed","part_type":"hotels","total_chunks":23,"total_response_length":3860}
{"time":"2025-12-08T17:22:19.505644Z","level":"INFO","msg":"Saved LLM response to cache","part_type":"hotels","cache_key":"a7c52c90e369a2c6457e9bd3ec8b436c_hotels","response_length":3860}
{"time":"2025-12-08T17:22:19.505679Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:22:19.506339Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":3}
{"time":"2025-12-08T17:22:19.58821Z","level":"INFO","msg":"Found existing city","city":"London","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd"}
{"time":"2025-12-08T17:22:19.630481Z","level":"INFO","msg":"Successfully saved interaction","interaction_id":"66433d29-5541-4f60-91db-2fa3cde243c5"}
{"time":"2025-12-08T17:22:19.950978Z","level":"INFO","msg":"Successfully saved initial itinerary to session","poi_count":3,"top_level_pois":3}
{"time":"2025-12-08T17:22:19.951141Z","level":"INFO","msg":"Processing unified response for POI extraction","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd","response_parts":1}
{"time":"2025-12-08T17:22:19.951166Z","level":"INFO","msg":"Processing hotels from unified response","content_length":3860}
{"time":"2025-12-08T17:22:19.951321Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:22:19.951341Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951533Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The Trafalgar St. James","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.951557Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951571Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"Strand Palace Hotel","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.95158Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951589Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The Z Hotel Piccadilly","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.951595Z","level":"INFO","msg":"Saved hotels from unified response","hotel_count":3}
{"time":"2025-12-08T17:22:20.052113Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:22:20.052139Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:22:20.052145Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"d712bf5e-aa9a-404f-ab2c-e19adc33c5eb","type":"complete","error":""}
{"time":"2025-12-08T17:22:21.271373Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels"}
{"time":"2025-12-08T17:22:21.271422Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels","prompt_length":1166}
2025/12/08 17:22:21 INFO Cache key provided but currently ignored in direct implementation cacheKey=ba495696e129b0e974e0f6b2ae96ef6c_hotels
{"time":"2025-12-08T17:22:21.753237Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:22:22.056079Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":2,"chunk_length":29,"chunk_preview":" coordinates (0.0000, 0.0000)"}
{"time":"2025-12-08T17:22:22.35954Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":3,"chunk_length":266,"chunk_preview":" are located in the middle of the Atlantic Ocean, off the coast of West Africa. However, since the r"}
{"time":"2025-12-08T17:22:28.729872Z","level":"INFO","msg":"LLM streaming completed","part_type":"hotels","total_chunks":23,"total_response_length":3831}
{"time":"2025-12-08T17:22:28.729939Z","level":"INFO","msg":"Saved LLM response to cache","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels","response_length":3831}
{"time":"2025-12-08T17:22:28.729959Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:22:28.730527Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":3}
{"time":"2025-12-08T17:22:28.779508Z","level":"INFO","msg":"Found existing city","city":"London","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd"}
{"time":"2025-12-08T17:22:28.815477Z","level":"INFO","msg":"Successfully saved interaction","interaction_id":"32f5c95a-07ac-4959-b604-c4c5618ebd59"}
{"time":"2025-12-08T17:22:29.134097Z","level":"INFO","msg":"Successfully saved initial itinerary to session","poi_count":3,"top_level_pois":3}
{"time":"2025-12-08T17:22:29.134179Z","level":"INFO","msg":"Processing unified response for POI extraction","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd","response_parts":1}
{"time":"2025-12-08T17:22:29.134251Z","level":"INFO","msg":"Processing hotels from unified response","content_length":3831}
{"time":"2025-12-08T17:22:29.134329Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:22:29.134415Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134442Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"Novotel London Canary Wharf","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.13446Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134472Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"DoubleTree by Hilton Hotel London - Greenwich","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.134483Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134648Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The O2 Arena Hotel (InterContinental London - The O2)","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.134661Z","level":"INFO","msg":"Saved hotels from unified response","hotel_count":3}
{"time":"2025-12-08T17:22:29.235272Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:22:29.235336Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:22:29.235349Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"6a9f6a9a-ba32-4a5c-9472-9ace729fda43","type":"complete","error":""}

The page changes to Hotels but doesnt show anything. 

For activities:

The network replies with 

   -{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZCIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY3Rpdml0aWVzIiwic2Vzc2lvbl9pZCI6ImY4YjM5OTVmLTQxYTktNGE1Yy1iYmI4LWUzZTk4ZTM2OTIxNyJ9",
    "timestamp": "2025-12-08T17:23:20.480430Z",
    "eventId": "4455276f-2a35-4d01-a072-68336200453a"
}   -{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZCIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY3Rpdml0aWVzIiwic2Vzc2lvbl9pZCI6ImY4YjM5OTVmLTQxYTktNGE1Yy1iYmI4LWUzZTk4ZTM2OTIxNyJ9",
    "timestamp": "2025-12-08T17:23:20.480430Z",
    "eventId": "4455276f-2a35-4d01-a072-68336200453a"
}   -{
    "type": "start",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZCIsImNpdHkiOiJMb25kb24iLCJkb21haW4iOiJhY3Rpdml0aWVzIiwic2Vzc2lvbl9pZCI6ImY4YjM5OTVmLTQxYTktNGE1Yy1iYmI4LWUzZTk4ZTM2OTIxNyJ9",
    "timestamp": "2025-12-08T17:23:20.480430Z",
    "eventId": "4455276f-2a35-4d01-a072-68336200453a"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiVGhlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.202419Z",
    "eventId": "441b7caa-653d-4383-ac1c-b9cee8ba7813"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiVGhlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.202419Z",
    "eventId": "441b7caa-653d-4383-ac1c-b9cee8ba7813"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiVGhlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.202419Z",
    "eventId": "441b7caa-653d-4383-ac1c-b9cee8ba7813"
}   Q{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGNvb3JkaW5hdGVzICgwLjAwMDAsIDAuMDAwMCkgYXJlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.505855Z",
    "eventId": "e9d30197-8444-4d15-918d-9da0debd575f"
}   Q{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGNvb3JkaW5hdGVzICgwLjAwMDAsIDAuMDAwMCkgYXJlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.505855Z",
    "eventId": "e9d30197-8444-4d15-918d-9da0debd575f"
}   Q{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGNvb3JkaW5hdGVzICgwLjAwMDAsIDAuMDAwMCkgYXJlIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:21.505855Z",
    "eventId": "e9d30197-8444-4d15-918d-9da0debd575f"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGxvY2F0ZWQgaW4gdGhlIG1pZGRsZSBvZiB0aGUgQXRsYW50aWMgT2NlYW4sIHNwZWNpZmljYWxseSBuZWFyIHRoZSBpbnRlcnNlY3Rpb24gb2YgdGhlIFByaW1lIE1lcmlkaWFuICgwwrAgbG9uZ2l0dWRlKSBhbmQgdGhlIEVxdWF0b3IgKDDCsCBsYXRpdHVkZSksIG9mdGVuIHJlZmVycmVkIHRvIGFzIE51bGwgSXNsYW5kLlxuXG5TaW5jZSBMb25kb24gaXMgbG9jYXRlZCBhdCBhcHByb3hpbWF0ZWx5IDUxLiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:21.807411Z",
    "eventId": "d9ce5932-7931-4120-8ebd-e16857ab5074"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGxvY2F0ZWQgaW4gdGhlIG1pZGRsZSBvZiB0aGUgQXRsYW50aWMgT2NlYW4sIHNwZWNpZmljYWxseSBuZWFyIHRoZSBpbnRlcnNlY3Rpb24gb2YgdGhlIFByaW1lIE1lcmlkaWFuICgwwrAgbG9uZ2l0dWRlKSBhbmQgdGhlIEVxdWF0b3IgKDDCsCBsYXRpdHVkZSksIG9mdGVuIHJlZmVycmVkIHRvIGFzIE51bGwgSXNsYW5kLlxuXG5TaW5jZSBMb25kb24gaXMgbG9jYXRlZCBhdCBhcHByb3hpbWF0ZWx5IDUxLiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:21.807411Z",
    "eventId": "d9ce5932-7931-4120-8ebd-e16857ab5074"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGxvY2F0ZWQgaW4gdGhlIG1pZGRsZSBvZiB0aGUgQXRsYW50aWMgT2NlYW4sIHNwZWNpZmljYWxseSBuZWFyIHRoZSBpbnRlcnNlY3Rpb24gb2YgdGhlIFByaW1lIE1lcmlkaWFuICgwwrAgbG9uZ2l0dWRlKSBhbmQgdGhlIEVxdWF0b3IgKDDCsCBsYXRpdHVkZSksIG9mdGVuIHJlZmVycmVkIHRvIGFzIE51bGwgSXNsYW5kLlxuXG5TaW5jZSBMb25kb24gaXMgbG9jYXRlZCBhdCBhcHByb3hpbWF0ZWx5IDUxLiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:21.807411Z",
    "eventId": "d9ce5932-7931-4120-8ebd-e16857ab5074"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiNTA3NMKwIE4sIDAuMTI3OMKwIFcsIHRoZSByZXF1ZXN0ZWQgY29vcmRpbmF0ZXMgYXJlICoqbm90IGluIExvbmRvbioqLlxuXG5Ib3dldmVyLCBiYXNlZCBvbiB5b3VyIHJlcXVlc3QgdG8gZmluZCBhY3Rpdml0aWVzICppbiBMb25kb24qIG5lYXIgdGhlICpnaXZlbiBjb29yZGluYXRlcyosIEkgbXVzdCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.110845Z",
    "eventId": "b257e212-8a02-4482-853a-e574b10e13d8"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiNTA3NMKwIE4sIDAuMTI3OMKwIFcsIHRoZSByZXF1ZXN0ZWQgY29vcmRpbmF0ZXMgYXJlICoqbm90IGluIExvbmRvbioqLlxuXG5Ib3dldmVyLCBiYXNlZCBvbiB5b3VyIHJlcXVlc3QgdG8gZmluZCBhY3Rpdml0aWVzICppbiBMb25kb24qIG5lYXIgdGhlICpnaXZlbiBjb29yZGluYXRlcyosIEkgbXVzdCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.110845Z",
    "eventId": "b257e212-8a02-4482-853a-e574b10e13d8"
}   	{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiNTA3NMKwIE4sIDAuMTI3OMKwIFcsIHRoZSByZXF1ZXN0ZWQgY29vcmRpbmF0ZXMgYXJlICoqbm90IGluIExvbmRvbioqLlxuXG5Ib3dldmVyLCBiYXNlZCBvbiB5b3VyIHJlcXVlc3QgdG8gZmluZCBhY3Rpdml0aWVzICppbiBMb25kb24qIG5lYXIgdGhlICpnaXZlbiBjb29yZGluYXRlcyosIEkgbXVzdCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.110845Z",
    "eventId": "b257e212-8a02-4482-853a-e574b10e13d8"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGFzc3VtZSB0aGVyZSB3YXMgYW4gZXJyb3IgaW4gdGhlIHByb3ZpZGVkIGNvb3JkaW5hdGVzIGFuZCB0aGF0IHlvdSBpbnRlbmRlZCB0byBzZWFyY2ggZm9yIGFjdGl2aXRpZXMgKmluIExvbmRvbiouIEkgd2lsbCB1c2UgYSBjZW50cmFsIExvbmRvbiBjb29yZGluYXRlIChlLmcuLCBUcmFmYWxnYXIgU3F1YXJlOiA1MS41MDgwLCAtMC4xMjgiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:22.413746Z",
    "eventId": "dfa5141e-0f88-456f-ac71-15c998332fc3"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGFzc3VtZSB0aGVyZSB3YXMgYW4gZXJyb3IgaW4gdGhlIHByb3ZpZGVkIGNvb3JkaW5hdGVzIGFuZCB0aGF0IHlvdSBpbnRlbmRlZCB0byBzZWFyY2ggZm9yIGFjdGl2aXRpZXMgKmluIExvbmRvbiouIEkgd2lsbCB1c2UgYSBjZW50cmFsIExvbmRvbiBjb29yZGluYXRlIChlLmcuLCBUcmFmYWxnYXIgU3F1YXJlOiA1MS41MDgwLCAtMC4xMjgiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:22.413746Z",
    "eventId": "dfa5141e-0f88-456f-ac71-15c998332fc3"
}   ){
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGFzc3VtZSB0aGVyZSB3YXMgYW4gZXJyb3IgaW4gdGhlIHByb3ZpZGVkIGNvb3JkaW5hdGVzIGFuZCB0aGF0IHlvdSBpbnRlbmRlZCB0byBzZWFyY2ggZm9yIGFjdGl2aXRpZXMgKmluIExvbmRvbiouIEkgd2lsbCB1c2UgYSBjZW50cmFsIExvbmRvbiBjb29yZGluYXRlIChlLmcuLCBUcmFmYWxnYXIgU3F1YXJlOiA1MS41MDgwLCAtMC4xMjgiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:22.413746Z",
    "eventId": "dfa5141e-0f88-456f-ac71-15c998332fc3"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiMSkgYXMgdGhlIGNlbnRlciBwb2ludCBmb3IgdGhlIHNlYXJjaCByYWRpdXMgb2YgNS4wIGttLCBhcyBzZWFyY2hpbmcgbmVhciAwLjAwMDAsIDAuMDAwMCB3b3VsZCB5aWVsZCBubyByZXN1bHRzIGluIExvbmRvbi5cblxuSGVyZSBhcmUgc29tZSBhY3Rpdml0aWVzIHdpdGhpbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.715955Z",
    "eventId": "3c8764f6-3bcd-459f-94fb-f336c4382ba3"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiMSkgYXMgdGhlIGNlbnRlciBwb2ludCBmb3IgdGhlIHNlYXJjaCByYWRpdXMgb2YgNS4wIGttLCBhcyBzZWFyY2hpbmcgbmVhciAwLjAwMDAsIDAuMDAwMCB3b3VsZCB5aWVsZCBubyByZXN1bHRzIGluIExvbmRvbi5cblxuSGVyZSBhcmUgc29tZSBhY3Rpdml0aWVzIHdpdGhpbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.715955Z",
    "eventId": "3c8764f6-3bcd-459f-94fb-f336c4382ba3"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiMSkgYXMgdGhlIGNlbnRlciBwb2ludCBmb3IgdGhlIHNlYXJjaCByYWRpdXMgb2YgNS4wIGttLCBhcyBzZWFyY2hpbmcgbmVhciAwLjAwMDAsIDAuMDAwMCB3b3VsZCB5aWVsZCBubyByZXN1bHRzIGluIExvbmRvbi5cblxuSGVyZSBhcmUgc29tZSBhY3Rpdml0aWVzIHdpdGhpbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:22.715955Z",
    "eventId": "3c8764f6-3bcd-459f-94fb-f336c4382ba3"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGEgNS4wIGttIHJhZGl1cyBvZiBjZW50cmFsIExvbmRvbiwgYWRoZXJpbmcgdG8geW91ciBwcmVmZXJlbmNlIGZvciBhIGJ1ZGdldCBsZXZlbCBvZiAwIChhbnkgYnVkZ2V0KTpcblxuYGBganNvblxue1xuICAgIFwiYWN0aXZpdGllc1wiOiBbXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.018897Z",
    "eventId": "8604fe17-5d0c-4759-8ccb-9a701a95ad94"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGEgNS4wIGttIHJhZGl1cyBvZiBjZW50cmFsIExvbmRvbiwgYWRoZXJpbmcgdG8geW91ciBwcmVmZXJlbmNlIGZvciBhIGJ1ZGdldCBsZXZlbCBvZiAwIChhbnkgYnVkZ2V0KTpcblxuYGBganNvblxue1xuICAgIFwiYWN0aXZpdGllc1wiOiBbXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.018897Z",
    "eventId": "8604fe17-5d0c-4759-8ccb-9a701a95ad94"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGEgNS4wIGttIHJhZGl1cyBvZiBjZW50cmFsIExvbmRvbiwgYWRoZXJpbmcgdG8geW91ciBwcmVmZXJlbmNlIGZvciBhIGJ1ZGdldCBsZXZlbCBvZiAwIChhbnkgYnVkZ2V0KTpcblxuYGBganNvblxue1xuICAgIFwiYWN0aXZpdGllc1wiOiBbXG4gICAgICAgIHtcbiAgICAgICAgICAgIFwiY2l0eVwiOiBcIkxvbmRvblwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.018897Z",
    "eventId": "8604fe17-5d0c-4759-8ccb-9a701a95ad94"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJUaGUgQnJpdGlzaCBNdXNldW1cIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTE5NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNzAsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiTXVzZXVtXCIsXG4gICAgICAgICAgICBcImRlc2NyaXB0aW9uXCI6IFwiRXhwbG9yZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.322696Z",
    "eventId": "ef06b76b-0b90-4169-b9c1-a80d9d0bec22"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJUaGUgQnJpdGlzaCBNdXNldW1cIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTE5NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNzAsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiTXVzZXVtXCIsXG4gICAgICAgICAgICBcImRlc2NyaXB0aW9uXCI6IFwiRXhwbG9yZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.322696Z",
    "eventId": "ef06b76b-0b90-4169-b9c1-a80d9d0bec22"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJUaGUgQnJpdGlzaCBNdXNldW1cIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTE5NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjEyNzAsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiTXVzZXVtXCIsXG4gICAgICAgICAgICBcImRlc2NyaXB0aW9uXCI6IFwiRXhwbG9yZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.322696Z",
    "eventId": "ef06b76b-0b90-4169-b9c1-a80d9d0bec22"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:23.625264Z",
    "eventId": "2e173588-1116-4698-bfe4-c4502f95df10"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:23.625264Z",
    "eventId": "2e173588-1116-4698-bfe4-c4502f95df10"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLlwiLFxuICAgICAgICAgICAgXCJhZGRyZXNzXCI6IFwiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:23.625264Z",
    "eventId": "2e173588-1116-4698-bfe4-c4502f95df10"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKVwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.927990Z",
    "eventId": "257e5846-a2ec-450b-a8a5-e3770d41800a"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKVwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.927990Z",
    "eventId": "257e5846-a2ec-450b-a8a5-e3770d41800a"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjogXCJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKVwiLCIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:23.927990Z",
    "eventId": "257e5846-a2ec-450b-a8a5-e3770d41800a"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC43LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkhpc3RvcnlcIixcbiAgICAgICAgICAgICAgICBcIkN1bHR1cmVcIixcbiAgICAgICAgICAgICAgICBcIkluZG9vclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcIiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.228887Z",
    "eventId": "7522546e-73d8-414c-829b-30561f4c6bdb"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC43LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkhpc3RvcnlcIixcbiAgICAgICAgICAgICAgICBcIkN1bHR1cmVcIixcbiAgICAgICAgICAgICAgICBcIkluZG9vclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcIiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.228887Z",
    "eventId": "7522546e-73d8-414c-829b-30561f4c6bdb"
}   i{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBcInByaWNlX3JhbmdlXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC43LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkhpc3RvcnlcIixcbiAgICAgICAgICAgICAgICBcIkN1bHR1cmVcIixcbiAgICAgICAgICAgICAgICBcIkluZG9vclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcIiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.228887Z",
    "eventId": "7522546e-73d8-414c-829b-30561f4c6bdb"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiZGlzdGFuY2VcIjogMS4xMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnlcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA4MCxcbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.532388Z",
    "eventId": "c72e42b8-3c52-45ad-bcea-947e3f543418"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiZGlzdGFuY2VcIjogMS4xMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnlcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA4MCxcbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.532388Z",
    "eventId": "c72e42b8-3c52-45ad-bcea-947e3f543418"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiZGlzdGFuY2VcIjogMS4xMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnlcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA4MCxcbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:24.532388Z",
    "eventId": "c72e42b8-3c52-45ad-bcea-947e3f543418"
}   u{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMTI4MSxcbiAgICAgICAgICAgIFwiY2F0ZWdvcnlcIjogXCJDdWx0dXJhbFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIlZpc2l0IHRoZSBpY29uaWMgcHVibGljIHNxdWFyZSBmZWF0dXJpbmcgTmVsc29uJ3MgQ29sdW1uIGFuZCB0aGUgTmF0aW9uYWwgR2FsbGVyeSwgd2hpY2ggaG91c2VzIGEgdmFzdCBjb2xsZWN0aW9uIG9mIFdlc3Rlcm4gRXVyb3BlYW4gcGFpbnRpbmdzIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:24.836031Z",
    "eventId": "0a83d25f-5a49-4eb1-82bc-d35c5505140c"
}   u{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMTI4MSxcbiAgICAgICAgICAgIFwiY2F0ZWdvcnlcIjogXCJDdWx0dXJhbFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIlZpc2l0IHRoZSBpY29uaWMgcHVibGljIHNxdWFyZSBmZWF0dXJpbmcgTmVsc29uJ3MgQ29sdW1uIGFuZCB0aGUgTmF0aW9uYWwgR2FsbGVyeSwgd2hpY2ggaG91c2VzIGEgdmFzdCBjb2xsZWN0aW9uIG9mIFdlc3Rlcm4gRXVyb3BlYW4gcGFpbnRpbmdzIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:24.836031Z",
    "eventId": "0a83d25f-5a49-4eb1-82bc-d35c5505140c"
}   u{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiICAgICAgICAgICAgXCJsb25naXR1ZGVcIjogLTAuMTI4MSxcbiAgICAgICAgICAgIFwiY2F0ZWdvcnlcIjogXCJDdWx0dXJhbFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIlZpc2l0IHRoZSBpY29uaWMgcHVibGljIHNxdWFyZSBmZWF0dXJpbmcgTmVsc29uJ3MgQ29sdW1uIGFuZCB0aGUgTmF0aW9uYWwgR2FsbGVyeSwgd2hpY2ggaG91c2VzIGEgdmFzdCBjb2xsZWN0aW9uIG9mIFdlc3Rlcm4gRXVyb3BlYW4gcGFpbnRpbmdzIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:24.836031Z",
    "eventId": "0a83d25f-5a49-4eb1-82bc-d35c5505140c"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLiBFbnRyeSB0byB0aGUgbWFpbiBjb2xsZWN0aW9uIGlzIGZyZWUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJUcmFmYWxnYXIgU3F1YXJlLCBMb25kb24gV0MyTiA1RE5cIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmciLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.138880Z",
    "eventId": "b34ee275-5725-4f0b-8826-c817e4d7ff08"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLiBFbnRyeSB0byB0aGUgbWFpbiBjb2xsZWN0aW9uIGlzIGZyZWUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJUcmFmYWxnYXIgU3F1YXJlLCBMb25kb24gV0MyTiA1RE5cIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmciLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.138880Z",
    "eventId": "b34ee275-5725-4f0b-8826-c817e4d7ff08"
}   -{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLiBFbnRyeSB0byB0aGUgbWFpbiBjb2xsZWN0aW9uIGlzIGZyZWUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJUcmFmYWxnYXIgU3F1YXJlLCBMb25kb24gV0MyTiA1RE5cIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmciLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.138880Z",
    "eventId": "b34ee275-5725-4f0b-8826-c817e4d7ff08"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiX2hvdXJzXCI6IFwiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:25.442197Z",
    "eventId": "926de35b-cd49-4415-8509-01659629cd90"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiX2hvdXJzXCI6IFwiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:25.442197Z",
    "eventId": "926de35b-cd49-4415-8509-01659629cd90"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiX2hvdXJzXCI6IFwiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:25.442197Z",
    "eventId": "926de35b-cd49-4415-8509-01659629cd90"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkFydFwiLFxuICAgICAgICAgICAgICAgIFwiSWNvbmljXCIsXG4gICAgICAgICAgICAgICAgXCJDZW50cmFsXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2UiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.746506Z",
    "eventId": "393af932-e0e1-428b-af09-e61625270c45"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkFydFwiLFxuICAgICAgICAgICAgICAgIFwiSWNvbmljXCIsXG4gICAgICAgICAgICAgICAgXCJDZW50cmFsXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2UiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.746506Z",
    "eventId": "393af932-e0e1-428b-af09-e61625270c45"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IFwiRnJlZVwiLFxuICAgICAgICAgICAgXCJyYXRpbmdcIjogNC42LFxuICAgICAgICAgICAgXCJ0YWdzXCI6IFtcbiAgICAgICAgICAgICAgICBcIkFydFwiLFxuICAgICAgICAgICAgICAgIFwiSWNvbmljXCIsXG4gICAgICAgICAgICAgICAgXCJDZW50cmFsXCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2UiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:25.746506Z",
    "eventId": "393af932-e0e1-428b-af09-e61625270c45"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IDAuMDBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJIeWRlIFBhcmtcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA3NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.049056Z",
    "eventId": "10837035-8879-4c03-a547-b6bc0898d6cc"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IDAuMDBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJIeWRlIFBhcmtcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA3NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.049056Z",
    "eventId": "10837035-8879-4c03-a547-b6bc0898d6cc"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXCI6IDAuMDBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJIeWRlIFBhcmtcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTA3NCxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.049056Z",
    "eventId": "10837035-8879-4c03-a547-b6bc0898d6cc"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIC0wLjE2NTcsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiT3V0ZG9vciBBY3Rpdml0eVwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgdmFzdCBSb3lhbCBQYXJrIHBlcmZlY3QgZm9yIHdhbGtpbmcsIHJlbGF4aW5nLCBvciBzaW1wbHkgZW5qb3lpbmcgZ3JlZW4gc3BhY2UgaW4gdGhlIGhlYXJ0IG9mIHRoZSBjaXR5LiBNYW55IHBhdGhzIGF2YWlsYWJsZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.352358Z",
    "eventId": "44e3e434-7c97-4769-bc62-56e118ad71db"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIC0wLjE2NTcsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiT3V0ZG9vciBBY3Rpdml0eVwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgdmFzdCBSb3lhbCBQYXJrIHBlcmZlY3QgZm9yIHdhbGtpbmcsIHJlbGF4aW5nLCBvciBzaW1wbHkgZW5qb3lpbmcgZ3JlZW4gc3BhY2UgaW4gdGhlIGhlYXJ0IG9mIHRoZSBjaXR5LiBNYW55IHBhdGhzIGF2YWlsYWJsZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.352358Z",
    "eventId": "44e3e434-7c97-4769-bc62-56e118ad71db"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIC0wLjE2NTcsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiT3V0ZG9vciBBY3Rpdml0eVwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgdmFzdCBSb3lhbCBQYXJrIHBlcmZlY3QgZm9yIHdhbGtpbmcsIHJlbGF4aW5nLCBvciBzaW1wbHkgZW5qb3lpbmcgZ3JlZW4gc3BhY2UgaW4gdGhlIGhlYXJ0IG9mIHRoZSBjaXR5LiBNYW55IHBhdGhzIGF2YWlsYWJsZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.352358Z",
    "eventId": "44e3e434-7c97-4769-bc62-56e118ad71db"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGZvciBsZWlzdXJlbHkgc3Ryb2xscy5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIkxvbmRvbiBXMiAyVUhcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LnJveWFscGFya3Mub3JnLnVrL3BhcmtzL2h5ZGUtcGFya1wiLFxuICAgICAgICAgICAgXCJvcGVuaW5nX2hvdXJzXCI6IFwiT3BlbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.655353Z",
    "eventId": "c4edc48b-4362-449f-ab5a-353be07b78e8"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGZvciBsZWlzdXJlbHkgc3Ryb2xscy5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIkxvbmRvbiBXMiAyVUhcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LnJveWFscGFya3Mub3JnLnVrL3BhcmtzL2h5ZGUtcGFya1wiLFxuICAgICAgICAgICAgXCJvcGVuaW5nX2hvdXJzXCI6IFwiT3BlbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.655353Z",
    "eventId": "c4edc48b-4362-449f-ab5a-353be07b78e8"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGZvciBsZWlzdXJlbHkgc3Ryb2xscy5cIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIkxvbmRvbiBXMiAyVUhcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LnJveWFscGFya3Mub3JnLnVrL3BhcmtzL2h5ZGUtcGFya1wiLFxuICAgICAgICAgICAgXCJvcGVuaW5nX2hvdXJzXCI6IFwiT3BlbiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:26.655353Z",
    "eventId": "c4edc48b-4362-449f-ab5a-353be07b78e8"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodFwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIkZyZWVcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNixcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJQYXJrXCIsXG4gICAgICAgICAgICAgICAgXCJSZWxheGluZ1wiLFxuICAgICAgICAgICAgICAgIFwiV2Fsa2luZ1wiIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.957097Z",
    "eventId": "ac56505c-7a58-44a1-96c4-b889c7556cda"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodFwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIkZyZWVcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNixcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJQYXJrXCIsXG4gICAgICAgICAgICAgICAgXCJSZWxheGluZ1wiLFxuICAgICAgICAgICAgICAgIFwiV2Fsa2luZ1wiIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.957097Z",
    "eventId": "ac56505c-7a58-44a1-96c4-b889c7556cda"
}   E{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodFwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIkZyZWVcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNixcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJQYXJrXCIsXG4gICAgICAgICAgICAgICAgXCJSZWxheGluZ1wiLFxuICAgICAgICAgICAgICAgIFwiV2Fsa2luZ1wiIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:26.957097Z",
    "eventId": "ac56505c-7a58-44a1-96c4-b889c7556cda"
}   ={
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIuNTBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJDb3ZlbnQgR2FyZGVuIE1hcmtldFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:27.260123Z",
    "eventId": "3c974533-1fd1-4b2f-9692-dccf5df4618f"
}   ={
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIuNTBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJDb3ZlbnQgR2FyZGVuIE1hcmtldFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:27.260123Z",
    "eventId": "3c974533-1fd1-4b2f-9692-dccf5df4618f"
}   ={
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIuNTBcbiAgICAgICAgfSxcbiAgICAgICAge1xuICAgICAgICAgICAgXCJjaXR5XCI6IFwiTG9uZG9uXCIsXG4gICAgICAgICAgICBcIm5hbWVcIjogXCJDb3ZlbnQgR2FyZGVuIE1hcmtldFwiLFxuICAgICAgICAgICAgXCJsYXRpdHVkZVwiOiIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:27.260123Z",
    "eventId": "3c974533-1fd1-4b2f-9692-dccf5df4618f"
}   %{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIDUxLjUxNDUsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjM2LFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkVudGVydGFpbm1lbnRcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.562621Z",
    "eventId": "c85914aa-5188-47c1-8486-da6fc002fd00"
}   %{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIDUxLjUxNDUsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjM2LFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkVudGVydGFpbm1lbnRcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.562621Z",
    "eventId": "c85914aa-5188-47c1-8486-da6fc002fd00"
}   %{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIDUxLjUxNDUsXG4gICAgICAgICAgICBcImxvbmdpdHVkZVwiOiAtMC4xMjM2LFxuICAgICAgICAgICAgXCJjYXRlZ29yeVwiOiBcIkVudGVydGFpbm1lbnRcIixcbiAgICAgICAgICAgIFwiZGVzY3JpcHRpb25cIjogXCJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.562621Z",
    "eventId": "c85914aa-5188-47c1-8486-da6fc002fd00"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkZcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LmNvdmVudGdhcmRlbi5sb25kb24vXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.865595Z",
    "eventId": "7da980f7-810f-40f9-9ed2-4b1ad8afde55"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkZcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LmNvdmVudGdhcmRlbi5sb25kb24vXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.865595Z",
    "eventId": "7da980f7-810f-40f9-9ed2-4b1ad8afde55"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuXCIsXG4gICAgICAgICAgICBcImFkZHJlc3NcIjogXCJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkZcIixcbiAgICAgICAgICAgIFwid2Vic2l0ZVwiOiBcImh0dHBzOi8vd3d3LmNvdmVudGdhcmRlbi5sb25kb24vXCIsXG4gICAgICAgICAgICBcIm9wZW5pbmdfIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:27.865595Z",
    "eventId": "7da980f7-810f-40f9-9ed2-4b1ad8afde55"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiaG91cnNcIjogXCJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS5cIixcbiAgICAgICAgICAgIFwicHJpY2VfcmFuZ2VcIjogXCIkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjQsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiU2hvcHBpbmdcIiwiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:28.169216Z",
    "eventId": "f2535485-21c3-4603-861e-43e0e4fa8a6b"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiaG91cnNcIjogXCJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS5cIixcbiAgICAgICAgICAgIFwicHJpY2VfcmFuZ2VcIjogXCIkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjQsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiU2hvcHBpbmdcIiwiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:28.169216Z",
    "eventId": "f2535485-21c3-4603-861e-43e0e4fa8a6b"
}   {
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiaG91cnNcIjogXCJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS5cIixcbiAgICAgICAgICAgIFwicHJpY2VfcmFuZ2VcIjogXCIkXCIsXG4gICAgICAgICAgICBcInJhdGluZ1wiOiA0LjQsXG4gICAgICAgICAgICBcInRhZ3NcIjogW1xuICAgICAgICAgICAgICAgIFwiU2hvcHBpbmdcIiwiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:28.169216Z",
    "eventId": "f2535485-21c3-4603-861e-43e0e4fa8a6b"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICAgICAgXCJTdHJlZXQgUGVyZm9ybWVyc1wiLFxuICAgICAgICAgICAgICAgIFwiTGl2ZWx5XCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMS4zMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:28.473294Z",
    "eventId": "98650b8f-36d3-4403-a1c7-5e984da2a8f9"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICAgICAgXCJTdHJlZXQgUGVyZm9ybWVyc1wiLFxuICAgICAgICAgICAgICAgIFwiTGl2ZWx5XCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMS4zMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:28.473294Z",
    "eventId": "98650b8f-36d3-4403-a1c7-5e984da2a8f9"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiXG4gICAgICAgICAgICAgICAgXCJTdHJlZXQgUGVyZm9ybWVyc1wiLFxuICAgICAgICAgICAgICAgIFwiTGl2ZWx5XCJcbiAgICAgICAgICAgIF0sXG4gICAgICAgICAgICBcImltYWdlc1wiOiBbXSxcbiAgICAgICAgICAgIFwiZGlzdGFuY2VcIjogMS4zMFxuICAgICAgICB9LFxuICAgICAgICB7XG4gICAgICAgICAgICBcImNpdHlcIjogXCJMb25kb25cIixcbiAgICAgICAgICAgIFwibmFtZVwiOiBcIlRoZSIsImRvbWFpbiI6ImFjdGl2aXRpZXMiLCJwYXJ0IjoiYWN0aXZpdGllcyJ9",
    "timestamp": "2025-12-08T17:23:28.473294Z",
    "eventId": "98650b8f-36d3-4403-a1c7-5e984da2a8f9"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIExvbmRvbiBFeWVcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTAzMyxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjExOTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiRW50ZXJ0YWlubWVudFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:28.777527Z",
    "eventId": "982e76da-3708-4b5f-99e6-51524931eaec"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIExvbmRvbiBFeWVcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTAzMyxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjExOTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiRW50ZXJ0YWlubWVudFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:28.777527Z",
    "eventId": "982e76da-3708-4b5f-99e6-51524931eaec"
}   1{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIExvbmRvbiBFeWVcIixcbiAgICAgICAgICAgIFwibGF0aXR1ZGVcIjogNTEuNTAzMyxcbiAgICAgICAgICAgIFwibG9uZ2l0dWRlXCI6IC0wLjExOTUsXG4gICAgICAgICAgICBcImNhdGVnb3J5XCI6IFwiRW50ZXJ0YWlubWVudFwiLFxuICAgICAgICAgICAgXCJkZXNjcmlwdGlvblwiOiBcIkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIiwiZG9tYWluIjoiYWN0aXZpdGllcyIsInBhcnQiOiJhY3Rpdml0aWVzIn0=",
    "timestamp": "2025-12-08T17:23:28.777527Z",
    "eventId": "982e76da-3708-4b5f-99e6-51524931eaec"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LilcIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjoiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.080360Z",
    "eventId": "8e3cd9dd-84da-496c-b0b6-6f9ba5192090"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LilcIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjoiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.080360Z",
    "eventId": "8e3cd9dd-84da-496c-b0b6-6f9ba5192090"
}   Y{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LilcIixcbiAgICAgICAgICAgIFwiYWRkcmVzc1wiOiBcIlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCXCIsXG4gICAgICAgICAgICBcIndlYnNpdGVcIjoiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.080360Z",
    "eventId": "8e3cd9dd-84da-496c-b0b6-6f9ba5192090"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIFwiaHR0cHM6Ly93d3cubG9uZG9uZXllLmNvbS9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.383565Z",
    "eventId": "bf0e8f38-6c08-462a-8de7-c55a091e69f6"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIFwiaHR0cHM6Ly93d3cubG9uZG9uZXllLmNvbS9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.383565Z",
    "eventId": "bf0e8f38-6c08-462a-8de7-c55a091e69f6"
}   �{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiIFwiaHR0cHM6Ly93d3cubG9uZG9uZXllLmNvbS9cIixcbiAgICAgICAgICAgIFwib3BlbmluZ19ob3Vyc1wiOiBcIlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLlwiLFxuICAgICAgICAgICAgXCJwcmljZV9yYW5nZVwiOiBcIiQiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.383565Z",
    "eventId": "bf0e8f38-6c08-462a-8de7-c55a091e69f6"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiJCRcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNCxcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJWaWV3XCIsXG4gICAgICAgICAgICAgICAgXCJUb3VyaXN0XCIsXG4gICAgICAgICAgICAgICAgXCJSaXZlclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.686409Z",
    "eventId": "f8e5039b-d5b2-491b-bc06-67e751dcaf4f"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiJCRcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNCxcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJWaWV3XCIsXG4gICAgICAgICAgICAgICAgXCJUb3VyaXN0XCIsXG4gICAgICAgICAgICAgICAgXCJSaXZlclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.686409Z",
    "eventId": "f8e5039b-d5b2-491b-bc06-67e751dcaf4f"
}   I{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiJCRcIixcbiAgICAgICAgICAgIFwicmF0aW5nXCI6IDQuNCxcbiAgICAgICAgICAgIFwidGFnc1wiOiBbXG4gICAgICAgICAgICAgICAgXCJWaWV3XCIsXG4gICAgICAgICAgICAgICAgXCJUb3VyaXN0XCIsXG4gICAgICAgICAgICAgICAgXCJSaXZlclwiXG4gICAgICAgICAgICBdLFxuICAgICAgICAgICAgXCJpbWFnZXNcIjogW10sXG4gICAgICAgICAgICBcImRpc3RhbmNlXCI6IDIiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.686409Z",
    "eventId": "f8e5039b-d5b2-491b-bc06-67e751dcaf4f"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLjIwXG4gICAgICAgIH1cbiAgICBdXG59XG5gYGAiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.990199Z",
    "eventId": "46b471a5-5bab-4e3b-ad8c-bdf39372ec7b"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLjIwXG4gICAgICAgIH1cbiAgICBdXG59XG5gYGAiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.990199Z",
    "eventId": "46b471a5-5bab-4e3b-ad8c-bdf39372ec7b"
}   M{
    "type": "chunk",
    "data": "eyJjYWNoZV9rZXkiOiJhODhlZWUzNDMxZjkyNzlhYmI2NGQ2OWVhNjg2ODBmZF9hY3Rpdml0aWVzIiwiY2FjaGVfdXNlZCI6ZmFsc2UsImNodW5rIjoiLjIwXG4gICAgICAgIH1cbiAgICBdXG59XG5gYGAiLCJkb21haW4iOiJhY3Rpdml0aWVzIiwicGFydCI6ImFjdGl2aXRpZXMifQ==",
    "timestamp": "2025-12-08T17:23:29.990199Z",
    "eventId": "46b471a5-5bab-4e3b-ad8c-bdf39372ec7b"
}   @�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIEJyaXRpc2ggTXVzZXVtIiwiZGlzdGFuY2UiOjEuMSwibGF0aXR1ZGUiOjUxLjUxOTQsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6Ik11c2V1bSIsImRlc2NyaXB0aW9uIjoiRXhwbG9yZSB3b3JsZCBoaXN0b3J5LCBhcnQsIGFuZCBjdWx0dXJlIHdpdGggbWlsbGlvbnMgb2YgYXJ0aWZhY3RzLCBpbmNsdWRpbmcgdGhlIFJvc2V0dGEgU3RvbmUgYW5kIEVsZ2luIE1hcmJsZXMuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNywiYWRkcmVzcyI6IkdyZWF0IFJ1c3NlbGwgU3QsIEJsb29tc2J1cnksIExvbmRvbiBXQzFCIDNERyIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJEYWlseSAxMDowMC0xNzowMCAoRnJpZGF5cyB1bnRpbCAyMDozMCkifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkhpc3RvcnkiLCJDdWx0dXJlIiwiSW5kb29yIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUcmFmYWxnYXIgU3F1YXJlIFx1MDAyNiBOYXRpb25hbCBHYWxsZXJ5IiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI4MSwiY2F0ZWdvcnkiOiJDdWx0dXJhbCIsImRlc2NyaXB0aW9uIjoiVmlzaXQgdGhlIGljb25pYyBwdWJsaWMgc3F1YXJlIGZlYXR1cmluZyBOZWxzb24ncyBDb2x1bW4gYW5kIHRoZSBOYXRpb25hbCBHYWxsZXJ5LCB3aGljaCBob3VzZXMgYSB2YXN0IGNvbGxlY3Rpb24gb2YgV2VzdGVybiBFdXJvcGVhbiBwYWludGluZ3MuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IlRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBXQzJOIDVETiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5uYXRpb25hbGdhbGxlcnkub3JnLnVrLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik5hdGlvbmFsIEdhbGxlcnk6IFNhdC1UaHUgMTA6MDAtMTg6MDAsIEZyaSAxMDowMC0yMTowMC4gU3F1YXJlIGlzIG9wZW4gMjQvNy4ifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkFydCIsIkljb25pYyIsIkNlbnRyYWwiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6Ikh5ZGUgUGFyayIsImRpc3RhbmNlIjoyLjUsImxhdGl0dWRlIjo1MS41MDc0LCJsb25naXR1ZGUiOi0wLjE2NTcsImNhdGVnb3J5IjoiT3V0ZG9vciBBY3Rpdml0eSIsImRlc2NyaXB0aW9uIjoiQSB2YXN0IFJveWFsIFBhcmsgcGVyZmVjdCBmb3Igd2Fsa2luZywgcmVsYXhpbmcsIG9yIHNpbXBseSBlbmpveWluZyBncmVlbiBzcGFjZSBpbiB0aGUgaGVhcnQgb2YgdGhlIGNpdHkuIE1hbnkgcGF0aHMgYXZhaWxhYmxlIGZvciBsZWlzdXJlbHkgc3Ryb2xscy4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IkxvbmRvbiBXMiAyVUgiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cucm95YWxwYXJrcy5vcmcudWsvcGFya3MvaHlkZS1wYXJrIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiT3BlbiBkYWlseSBmcm9tIDU6MDAgdW50aWwgbWlkbmlnaHQifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlBhcmsiLCJSZWxheGluZyIsIldhbGtpbmciXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IkNvdmVudCBHYXJkZW4gTWFya2V0IiwiZGlzdGFuY2UiOjEuMywibGF0aXR1ZGUiOjUxLjUxNDUsImxvbmdpdHVkZSI6LTAuMTIzNiwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkYiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cuY292ZW50Z2FyZGVuLmxvbmRvbi8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS4ifSwicHJpY2VfcmFuZ2UiOiIkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlNob3BwaW5nIiwiU3RyZWV0IFBlcmZvcm1lcnMiLCJMaXZlbHkiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRoZSBMb25kb24gRXllIiwiZGlzdGFuY2UiOjIuMiwibGF0aXR1ZGUiOjUxLjUwMzMsImxvbmdpdHVkZSI6LTAuMTE5NSwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGNhbnRpbGV2ZXJlZCBvYnNlcnZhdGlvbiB3aGVlbCBvbiB0aGUgU291dGggQmFuayBvZiB0aGUgUml2ZXIgVGhhbWVzLCBvZmZlcmluZyBzcGVjdGFjdWxhciBwYW5vcmFtaWMgdmlld3Mgb2YgdGhlIGNpdHkuIChOb3RlOiBUaGlzIGF0dHJhY3Rpb24gaGFzIGEgY29zdC4pIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJSaXZlcnNpZGUgQnVpbGRpbmcsIENvdW50eSBIYWxsLCBMb25kb24gU0UxIDdQQiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5sb25kb25leWUuY29tLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLiJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiOWIxNTZmNzktMDk0MC00MTc3LTk3NzctOWIzOTU3NTcxNzNhIiwidGFncyI6WyJWaWV3IiwiVG91cmlzdCIsIlJpdmVyIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9XSwiaXRpbmVyYXJ5X3Jlc3BvbnNlIjp7Iml0aW5lcmFyeV9uYW1lIjoiIiwib3ZlcmFsbF9kZXNjcmlwdGlvbiI6IiIsInBvaW50c19vZl9pbnRlcmVzdCI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dfSwiYWN0aXZpdGllcyI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dLCJzZXNzaW9uX2lkIjoiZjhiMzk5NWYtNDFhOS00YTVjLWJiYjgtZTNlOThlMzY5MjE3In0=",
    "timestamp": "2025-12-08T17:23:30.407276Z",
    "eventId": "275815c9-afb7-4b95-8359-0663d090526a"
}   @�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIEJyaXRpc2ggTXVzZXVtIiwiZGlzdGFuY2UiOjEuMSwibGF0aXR1ZGUiOjUxLjUxOTQsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6Ik11c2V1bSIsImRlc2NyaXB0aW9uIjoiRXhwbG9yZSB3b3JsZCBoaXN0b3J5LCBhcnQsIGFuZCBjdWx0dXJlIHdpdGggbWlsbGlvbnMgb2YgYXJ0aWZhY3RzLCBpbmNsdWRpbmcgdGhlIFJvc2V0dGEgU3RvbmUgYW5kIEVsZ2luIE1hcmJsZXMuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNywiYWRkcmVzcyI6IkdyZWF0IFJ1c3NlbGwgU3QsIEJsb29tc2J1cnksIExvbmRvbiBXQzFCIDNERyIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJEYWlseSAxMDowMC0xNzowMCAoRnJpZGF5cyB1bnRpbCAyMDozMCkifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkhpc3RvcnkiLCJDdWx0dXJlIiwiSW5kb29yIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUcmFmYWxnYXIgU3F1YXJlIFx1MDAyNiBOYXRpb25hbCBHYWxsZXJ5IiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI4MSwiY2F0ZWdvcnkiOiJDdWx0dXJhbCIsImRlc2NyaXB0aW9uIjoiVmlzaXQgdGhlIGljb25pYyBwdWJsaWMgc3F1YXJlIGZlYXR1cmluZyBOZWxzb24ncyBDb2x1bW4gYW5kIHRoZSBOYXRpb25hbCBHYWxsZXJ5LCB3aGljaCBob3VzZXMgYSB2YXN0IGNvbGxlY3Rpb24gb2YgV2VzdGVybiBFdXJvcGVhbiBwYWludGluZ3MuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IlRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBXQzJOIDVETiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5uYXRpb25hbGdhbGxlcnkub3JnLnVrLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik5hdGlvbmFsIEdhbGxlcnk6IFNhdC1UaHUgMTA6MDAtMTg6MDAsIEZyaSAxMDowMC0yMTowMC4gU3F1YXJlIGlzIG9wZW4gMjQvNy4ifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkFydCIsIkljb25pYyIsIkNlbnRyYWwiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6Ikh5ZGUgUGFyayIsImRpc3RhbmNlIjoyLjUsImxhdGl0dWRlIjo1MS41MDc0LCJsb25naXR1ZGUiOi0wLjE2NTcsImNhdGVnb3J5IjoiT3V0ZG9vciBBY3Rpdml0eSIsImRlc2NyaXB0aW9uIjoiQSB2YXN0IFJveWFsIFBhcmsgcGVyZmVjdCBmb3Igd2Fsa2luZywgcmVsYXhpbmcsIG9yIHNpbXBseSBlbmpveWluZyBncmVlbiBzcGFjZSBpbiB0aGUgaGVhcnQgb2YgdGhlIGNpdHkuIE1hbnkgcGF0aHMgYXZhaWxhYmxlIGZvciBsZWlzdXJlbHkgc3Ryb2xscy4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IkxvbmRvbiBXMiAyVUgiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cucm95YWxwYXJrcy5vcmcudWsvcGFya3MvaHlkZS1wYXJrIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiT3BlbiBkYWlseSBmcm9tIDU6MDAgdW50aWwgbWlkbmlnaHQifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlBhcmsiLCJSZWxheGluZyIsIldhbGtpbmciXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IkNvdmVudCBHYXJkZW4gTWFya2V0IiwiZGlzdGFuY2UiOjEuMywibGF0aXR1ZGUiOjUxLjUxNDUsImxvbmdpdHVkZSI6LTAuMTIzNiwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkYiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cuY292ZW50Z2FyZGVuLmxvbmRvbi8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS4ifSwicHJpY2VfcmFuZ2UiOiIkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlNob3BwaW5nIiwiU3RyZWV0IFBlcmZvcm1lcnMiLCJMaXZlbHkiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRoZSBMb25kb24gRXllIiwiZGlzdGFuY2UiOjIuMiwibGF0aXR1ZGUiOjUxLjUwMzMsImxvbmdpdHVkZSI6LTAuMTE5NSwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGNhbnRpbGV2ZXJlZCBvYnNlcnZhdGlvbiB3aGVlbCBvbiB0aGUgU291dGggQmFuayBvZiB0aGUgUml2ZXIgVGhhbWVzLCBvZmZlcmluZyBzcGVjdGFjdWxhciBwYW5vcmFtaWMgdmlld3Mgb2YgdGhlIGNpdHkuIChOb3RlOiBUaGlzIGF0dHJhY3Rpb24gaGFzIGEgY29zdC4pIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJSaXZlcnNpZGUgQnVpbGRpbmcsIENvdW50eSBIYWxsLCBMb25kb24gU0UxIDdQQiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5sb25kb25leWUuY29tLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLiJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiOWIxNTZmNzktMDk0MC00MTc3LTk3NzctOWIzOTU3NTcxNzNhIiwidGFncyI6WyJWaWV3IiwiVG91cmlzdCIsIlJpdmVyIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9XSwiaXRpbmVyYXJ5X3Jlc3BvbnNlIjp7Iml0aW5lcmFyeV9uYW1lIjoiIiwib3ZlcmFsbF9kZXNjcmlwdGlvbiI6IiIsInBvaW50c19vZl9pbnRlcmVzdCI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dfSwiYWN0aXZpdGllcyI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dLCJzZXNzaW9uX2lkIjoiZjhiMzk5NWYtNDFhOS00YTVjLWJiYjgtZTNlOThlMzY5MjE3In0=",
    "timestamp": "2025-12-08T17:23:30.407276Z",
    "eventId": "275815c9-afb7-4b95-8359-0663d090526a"
}   @�{
    "type": "itinerary",
    "data": "eyJnZW5lcmFsX2NpdHlfZGF0YSI6eyJjaXR5IjoiIiwiY291bnRyeSI6IiIsImRlc2NyaXB0aW9uIjoiIiwicG9wdWxhdGlvbiI6IiIsImFyZWEiOiIiLCJ0aW1lem9uZSI6IiIsImxhbmd1YWdlIjoiIiwid2VhdGhlciI6IiIsImF0dHJhY3Rpb25zIjoiIiwiaGlzdG9yeSI6IiJ9LCJwb2ludHNfb2ZfaW50ZXJlc3QiOlt7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIEJyaXRpc2ggTXVzZXVtIiwiZGlzdGFuY2UiOjEuMSwibGF0aXR1ZGUiOjUxLjUxOTQsImxvbmdpdHVkZSI6LTAuMTI3LCJjYXRlZ29yeSI6Ik11c2V1bSIsImRlc2NyaXB0aW9uIjoiRXhwbG9yZSB3b3JsZCBoaXN0b3J5LCBhcnQsIGFuZCBjdWx0dXJlIHdpdGggbWlsbGlvbnMgb2YgYXJ0aWZhY3RzLCBpbmNsdWRpbmcgdGhlIFJvc2V0dGEgU3RvbmUgYW5kIEVsZ2luIE1hcmJsZXMuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNywiYWRkcmVzcyI6IkdyZWF0IFJ1c3NlbGwgU3QsIEJsb29tc2J1cnksIExvbmRvbiBXQzFCIDNERyIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5icml0aXNobXVzZXVtLm9yZy8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJEYWlseSAxMDowMC0xNzowMCAoRnJpZGF5cyB1bnRpbCAyMDozMCkifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkhpc3RvcnkiLCJDdWx0dXJlIiwiSW5kb29yIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9LHsiaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUcmFmYWxnYXIgU3F1YXJlIFx1MDAyNiBOYXRpb25hbCBHYWxsZXJ5IiwiZGlzdGFuY2UiOjAsImxhdGl0dWRlIjo1MS41MDgsImxvbmdpdHVkZSI6LTAuMTI4MSwiY2F0ZWdvcnkiOiJDdWx0dXJhbCIsImRlc2NyaXB0aW9uIjoiVmlzaXQgdGhlIGljb25pYyBwdWJsaWMgc3F1YXJlIGZlYXR1cmluZyBOZWxzb24ncyBDb2x1bW4gYW5kIHRoZSBOYXRpb25hbCBHYWxsZXJ5LCB3aGljaCBob3VzZXMgYSB2YXN0IGNvbGxlY3Rpb24gb2YgV2VzdGVybiBFdXJvcGVhbiBwYWludGluZ3MuIEVudHJ5IHRvIHRoZSBtYWluIGNvbGxlY3Rpb24gaXMgZnJlZS4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IlRyYWZhbGdhciBTcXVhcmUsIExvbmRvbiBXQzJOIDVETiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5uYXRpb25hbGdhbGxlcnkub3JnLnVrLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik5hdGlvbmFsIEdhbGxlcnk6IFNhdC1UaHUgMTA6MDAtMTg6MDAsIEZyaSAxMDowMC0yMTowMC4gU3F1YXJlIGlzIG9wZW4gMjQvNy4ifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIkFydCIsIkljb25pYyIsIkNlbnRyYWwiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6Ikh5ZGUgUGFyayIsImRpc3RhbmNlIjoyLjUsImxhdGl0dWRlIjo1MS41MDc0LCJsb25naXR1ZGUiOi0wLjE2NTcsImNhdGVnb3J5IjoiT3V0ZG9vciBBY3Rpdml0eSIsImRlc2NyaXB0aW9uIjoiQSB2YXN0IFJveWFsIFBhcmsgcGVyZmVjdCBmb3Igd2Fsa2luZywgcmVsYXhpbmcsIG9yIHNpbXBseSBlbmpveWluZyBncmVlbiBzcGFjZSBpbiB0aGUgaGVhcnQgb2YgdGhlIGNpdHkuIE1hbnkgcGF0aHMgYXZhaWxhYmxlIGZvciBsZWlzdXJlbHkgc3Ryb2xscy4iLCJyYXRpbmciOjQuNiwiYWRkcmVzcyI6IkxvbmRvbiBXMiAyVUgiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cucm95YWxwYXJrcy5vcmcudWsvcGFya3MvaHlkZS1wYXJrIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiT3BlbiBkYWlseSBmcm9tIDU6MDAgdW50aWwgbWlkbmlnaHQifSwicHJpY2VfcmFuZ2UiOiJGcmVlIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlBhcmsiLCJSZWxheGluZyIsIldhbGtpbmciXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IkNvdmVudCBHYXJkZW4gTWFya2V0IiwiZGlzdGFuY2UiOjEuMywibGF0aXR1ZGUiOjUxLjUxNDUsImxvbmdpdHVkZSI6LTAuMTIzNiwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGxpdmVseSBhcmVhIGZlYXR1cmluZyBzdHJlZXQgcGVyZm9ybWVycywgdW5pcXVlIHNob3BzLCBhbmQgZGluaW5nIG9wdGlvbnMuIEdyZWF0IGZvciBwZW9wbGUtd2F0Y2hpbmcgYW5kIHNvYWtpbmcgdXAgdGhlIGF0bW9zcGhlcmUuIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJDb3ZlbnQgR2FyZGVuLCBMb25kb24gV0MyRSA4UkYiLCJwaG9uZV9udW1iZXIiOiIiLCJ3ZWJzaXRlIjoiaHR0cHM6Ly93d3cuY292ZW50Z2FyZGVuLmxvbmRvbi8iLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJNYXJrZXQgc3RhbGxzIHZhcnksIGdlbmVyYWxseSAxMDowMC0xOTowMCBkYWlseS4ifSwicHJpY2VfcmFuZ2UiOiIkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlNob3BwaW5nIiwiU3RyZWV0IFBlcmZvcm1lcnMiLCJMaXZlbHkiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRoZSBMb25kb24gRXllIiwiZGlzdGFuY2UiOjIuMiwibGF0aXR1ZGUiOjUxLjUwMzMsImxvbmdpdHVkZSI6LTAuMTE5NSwiY2F0ZWdvcnkiOiJFbnRlcnRhaW5tZW50IiwiZGVzY3JpcHRpb24iOiJBIGNhbnRpbGV2ZXJlZCBvYnNlcnZhdGlvbiB3aGVlbCBvbiB0aGUgU291dGggQmFuayBvZiB0aGUgUml2ZXIgVGhhbWVzLCBvZmZlcmluZyBzcGVjdGFjdWxhciBwYW5vcmFtaWMgdmlld3Mgb2YgdGhlIGNpdHkuIChOb3RlOiBUaGlzIGF0dHJhY3Rpb24gaGFzIGEgY29zdC4pIiwicmF0aW5nIjo0LjQsImFkZHJlc3MiOiJSaXZlcnNpZGUgQnVpbGRpbmcsIENvdW50eSBIYWxsLCBMb25kb24gU0UxIDdQQiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5sb25kb25leWUuY29tLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IlZhcmllcyBzaWduaWZpY2FudGx5IGJ5IHNlYXNvbiwgdHlwaWNhbGx5IDEwOjAwLTIwOjMwLiJ9LCJwcmljZV9yYW5nZSI6IiQkJCIsInByaWNlX2xldmVsIjoiIiwicmV2aWV3cyI6bnVsbCwibGxtX2ludGVyYWN0aW9uX2lkIjoiOWIxNTZmNzktMDk0MC00MTc3LTk3NzctOWIzOTU3NTcxNzNhIiwidGFncyI6WyJWaWV3IiwiVG91cmlzdCIsIlJpdmVyIl0sImNyZWF0ZWRfYXQiOiIwMDAxLTAxLTAxVDAwOjAwOjAwWiIsImFtZW5pdGllcyI6IiJ9XSwiaXRpbmVyYXJ5X3Jlc3BvbnNlIjp7Iml0aW5lcmFyeV9uYW1lIjoiIiwib3ZlcmFsbF9kZXNjcmlwdGlvbiI6IiIsInBvaW50c19vZl9pbnRlcmVzdCI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjE3OTNiZWIxLTgxZmMtNGIxMy1iMmZkLTEzYjQ2MTM3M2FmZCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMTc5M2JlYjEtODFmYy00YjEzLWIyZmQtMTNiNDYxMzczYWZkIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjliMTU2Zjc5LTA5NDAtNDE3Ny05Nzc3LTliMzk1NzU3MTczYSIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIxNzkzYmViMS04MWZjLTRiMTMtYjJmZC0xM2I0NjEzNzNhZmQiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiI5YjE1NmY3OS0wOTQwLTQxNzctOTc3Ny05YjM5NTc1NzE3M2EiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dfSwiYWN0aXZpdGllcyI6W3siaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJjaXR5IjoiTG9uZG9uIiwiY2l0eV9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsIm5hbWUiOiJUaGUgQnJpdGlzaCBNdXNldW0iLCJkaXN0YW5jZSI6MS4xLCJsYXRpdHVkZSI6NTEuNTE5NCwibG9uZ2l0dWRlIjotMC4xMjcsImNhdGVnb3J5IjoiTXVzZXVtIiwiZGVzY3JpcHRpb24iOiJFeHBsb3JlIHdvcmxkIGhpc3RvcnksIGFydCwgYW5kIGN1bHR1cmUgd2l0aCBtaWxsaW9ucyBvZiBhcnRpZmFjdHMsIGluY2x1ZGluZyB0aGUgUm9zZXR0YSBTdG9uZSBhbmQgRWxnaW4gTWFyYmxlcy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC43LCJhZGRyZXNzIjoiR3JlYXQgUnVzc2VsbCBTdCwgQmxvb21zYnVyeSwgTG9uZG9uIFdDMUIgM0RHIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmJyaXRpc2htdXNldW0ub3JnLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6IkRhaWx5IDEwOjAwLTE3OjAwIChGcmlkYXlzIHVudGlsIDIwOjMwKSJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiSGlzdG9yeSIsIkN1bHR1cmUiLCJJbmRvb3IiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn0seyJpZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsImNpdHkiOiJMb25kb24iLCJjaXR5X2lkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwibmFtZSI6IlRyYWZhbGdhciBTcXVhcmUgXHUwMDI2IE5hdGlvbmFsIEdhbGxlcnkiLCJkaXN0YW5jZSI6MCwibGF0aXR1ZGUiOjUxLjUwOCwibG9uZ2l0dWRlIjotMC4xMjgxLCJjYXRlZ29yeSI6IkN1bHR1cmFsIiwiZGVzY3JpcHRpb24iOiJWaXNpdCB0aGUgaWNvbmljIHB1YmxpYyBzcXVhcmUgZmVhdHVyaW5nIE5lbHNvbidzIENvbHVtbiBhbmQgdGhlIE5hdGlvbmFsIEdhbGxlcnksIHdoaWNoIGhvdXNlcyBhIHZhc3QgY29sbGVjdGlvbiBvZiBXZXN0ZXJuIEV1cm9wZWFuIHBhaW50aW5ncy4gRW50cnkgdG8gdGhlIG1haW4gY29sbGVjdGlvbiBpcyBmcmVlLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiVHJhZmFsZ2FyIFNxdWFyZSwgTG9uZG9uIFdDMk4gNUROIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3Lm5hdGlvbmFsZ2FsbGVyeS5vcmcudWsvIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiTmF0aW9uYWwgR2FsbGVyeTogU2F0LVRodSAxMDowMC0xODowMCwgRnJpIDEwOjAwLTIxOjAwLiBTcXVhcmUgaXMgb3BlbiAyNC83LiJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiQXJ0IiwiSWNvbmljIiwiQ2VudHJhbCJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiSHlkZSBQYXJrIiwiZGlzdGFuY2UiOjIuNSwibGF0aXR1ZGUiOjUxLjUwNzQsImxvbmdpdHVkZSI6LTAuMTY1NywiY2F0ZWdvcnkiOiJPdXRkb29yIEFjdGl2aXR5IiwiZGVzY3JpcHRpb24iOiJBIHZhc3QgUm95YWwgUGFyayBwZXJmZWN0IGZvciB3YWxraW5nLCByZWxheGluZywgb3Igc2ltcGx5IGVuam95aW5nIGdyZWVuIHNwYWNlIGluIHRoZSBoZWFydCBvZiB0aGUgY2l0eS4gTWFueSBwYXRocyBhdmFpbGFibGUgZm9yIGxlaXN1cmVseSBzdHJvbGxzLiIsInJhdGluZyI6NC42LCJhZGRyZXNzIjoiTG9uZG9uIFcyIDJVSCIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5yb3lhbHBhcmtzLm9yZy51ay9wYXJrcy9oeWRlLXBhcmsiLCJvcGVuaW5nX2hvdXJzIjp7ImdlbmVyYWwiOiJPcGVuIGRhaWx5IGZyb20gNTowMCB1bnRpbCBtaWRuaWdodCJ9LCJwcmljZV9yYW5nZSI6IkZyZWUiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiUGFyayIsIlJlbGF4aW5nIiwiV2Fsa2luZyJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiQ292ZW50IEdhcmRlbiBNYXJrZXQiLCJkaXN0YW5jZSI6MS4zLCJsYXRpdHVkZSI6NTEuNTE0NSwibG9uZ2l0dWRlIjotMC4xMjM2LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgbGl2ZWx5IGFyZWEgZmVhdHVyaW5nIHN0cmVldCBwZXJmb3JtZXJzLCB1bmlxdWUgc2hvcHMsIGFuZCBkaW5pbmcgb3B0aW9ucy4gR3JlYXQgZm9yIHBlb3BsZS13YXRjaGluZyBhbmQgc29ha2luZyB1cCB0aGUgYXRtb3NwaGVyZS4iLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IkNvdmVudCBHYXJkZW4sIExvbmRvbiBXQzJFIDhSRiIsInBob25lX251bWJlciI6IiIsIndlYnNpdGUiOiJodHRwczovL3d3dy5jb3ZlbnRnYXJkZW4ubG9uZG9uLyIsIm9wZW5pbmdfaG91cnMiOnsiZ2VuZXJhbCI6Ik1hcmtldCBzdGFsbHMgdmFyeSwgZ2VuZXJhbGx5IDEwOjAwLTE5OjAwIGRhaWx5LiJ9LCJwcmljZV9yYW5nZSI6IiQiLCJwcmljZV9sZXZlbCI6IiIsInJldmlld3MiOm51bGwsImxsbV9pbnRlcmFjdGlvbl9pZCI6IjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMCIsInRhZ3MiOlsiU2hvcHBpbmciLCJTdHJlZXQgUGVyZm9ybWVycyIsIkxpdmVseSJdLCJjcmVhdGVkX2F0IjoiMDAwMS0wMS0wMVQwMDowMDowMFoiLCJhbWVuaXRpZXMiOiIifSx7ImlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAwIiwiY2l0eSI6IkxvbmRvbiIsImNpdHlfaWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJuYW1lIjoiVGhlIExvbmRvbiBFeWUiLCJkaXN0YW5jZSI6Mi4yLCJsYXRpdHVkZSI6NTEuNTAzMywibG9uZ2l0dWRlIjotMC4xMTk1LCJjYXRlZ29yeSI6IkVudGVydGFpbm1lbnQiLCJkZXNjcmlwdGlvbiI6IkEgY2FudGlsZXZlcmVkIG9ic2VydmF0aW9uIHdoZWVsIG9uIHRoZSBTb3V0aCBCYW5rIG9mIHRoZSBSaXZlciBUaGFtZXMsIG9mZmVyaW5nIHNwZWN0YWN1bGFyIHBhbm9yYW1pYyB2aWV3cyBvZiB0aGUgY2l0eS4gKE5vdGU6IFRoaXMgYXR0cmFjdGlvbiBoYXMgYSBjb3N0LikiLCJyYXRpbmciOjQuNCwiYWRkcmVzcyI6IlJpdmVyc2lkZSBCdWlsZGluZywgQ291bnR5IEhhbGwsIExvbmRvbiBTRTEgN1BCIiwicGhvbmVfbnVtYmVyIjoiIiwid2Vic2l0ZSI6Imh0dHBzOi8vd3d3LmxvbmRvbmV5ZS5jb20vIiwib3BlbmluZ19ob3VycyI6eyJnZW5lcmFsIjoiVmFyaWVzIHNpZ25pZmljYW50bHkgYnkgc2Vhc29uLCB0eXBpY2FsbHkgMTA6MDAtMjA6MzAuIn0sInByaWNlX3JhbmdlIjoiJCQkIiwicHJpY2VfbGV2ZWwiOiIiLCJyZXZpZXdzIjpudWxsLCJsbG1faW50ZXJhY3Rpb25faWQiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJ0YWdzIjpbIlZpZXciLCJUb3VyaXN0IiwiUml2ZXIiXSwiY3JlYXRlZF9hdCI6IjAwMDEtMDEtMDFUMDA6MDA6MDBaIiwiYW1lbml0aWVzIjoiIn1dLCJzZXNzaW9uX2lkIjoiZjhiMzk5NWYtNDFhOS00YTVjLWJiYjgtZTNlOThlMzY5MjE3In0=",
    "timestamp": "2025-12-08T17:23:30.407276Z",
    "eventId": "275815c9-afb7-4b95-8359-0663d090526a"
}   �{
    "type": "complete",
    "data": "eyJzZXNzaW9uX2lkIjoiZjhiMzk5NWYtNDFhOS00YTVjLWJiYjgtZTNlOThlMzY5MjE3In0=",
    "timestamp": "2025-12-08T17:23:30.736257Z",
    "eventId": "0efa39f1-dd76-4132-9880-620f922cd75a",
    "navigation": {
        "url": "/activities?sessionId=f8b3995f-41a9-4a5c-bbb8-e3e98e369217&cityName=London&domain=activities",
        "routeType": "activities",
        "queryParams": {
            "cityName": "London",
            "domain": "activities",
            "sessionId": "f8b3995f-41a9-4a5c-bbb8-e3e98e369217"
        }
    }
}   {}

And the server logs:

{"time":"2025-12-08T17:22:19.951321Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:22:19.951341Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951533Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The Trafalgar St. James","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.951557Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951571Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"Strand Palace Hotel","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.95158Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:19.951589Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The Z Hotel Piccadilly","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:19.951595Z","level":"INFO","msg":"Saved hotels from unified response","hotel_count":3}
{"time":"2025-12-08T17:22:20.052113Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:22:20.052139Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:22:20.052145Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"d712bf5e-aa9a-404f-ab2c-e19adc33c5eb","type":"complete","error":""}
{"time":"2025-12-08T17:22:21.271373Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels"}
{"time":"2025-12-08T17:22:21.271422Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels","prompt_length":1166}
2025/12/08 17:22:21 INFO Cache key provided but currently ignored in direct implementation cacheKey=ba495696e129b0e974e0f6b2ae96ef6c_hotels
{"time":"2025-12-08T17:22:21.753237Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:22:22.056079Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":2,"chunk_length":29,"chunk_preview":" coordinates (0.0000, 0.0000)"}
{"time":"2025-12-08T17:22:22.35954Z","level":"INFO","msg":"Received chunk from LLM","part_type":"hotels","chunk_number":3,"chunk_length":266,"chunk_preview":" are located in the middle of the Atlantic Ocean, off the coast of West Africa. However, since the r"}
{"time":"2025-12-08T17:22:28.729872Z","level":"INFO","msg":"LLM streaming completed","part_type":"hotels","total_chunks":23,"total_response_length":3831}
{"time":"2025-12-08T17:22:28.729939Z","level":"INFO","msg":"Saved LLM response to cache","part_type":"hotels","cache_key":"ba495696e129b0e974e0f6b2ae96ef6c_hotels","response_length":3831}
{"time":"2025-12-08T17:22:28.729959Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:22:28.730527Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":3}
{"time":"2025-12-08T17:22:28.779508Z","level":"INFO","msg":"Found existing city","city":"London","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd"}
{"time":"2025-12-08T17:22:28.815477Z","level":"INFO","msg":"Successfully saved interaction","interaction_id":"32f5c95a-07ac-4959-b604-c4c5618ebd59"}
{"time":"2025-12-08T17:22:29.134097Z","level":"INFO","msg":"Successfully saved initial itinerary to session","poi_count":3,"top_level_pois":3}
{"time":"2025-12-08T17:22:29.134179Z","level":"INFO","msg":"Processing unified response for POI extraction","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd","response_parts":1}
{"time":"2025-12-08T17:22:29.134251Z","level":"INFO","msg":"Processing hotels from unified response","content_length":3831}
{"time":"2025-12-08T17:22:29.134329Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:22:29.134415Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134442Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"Novotel London Canary Wharf","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.13446Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134472Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"DoubleTree by Hilton Hotel London - Greenwich","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.134483Z","level":"WARN","msg":"Invalid JSON for opening_hours, setting to NULL","value":"24 hours"}
{"time":"2025-12-08T17:22:29.134648Z","level":"WARN","msg":"Failed to save hotel from unified response","hotel_name":"The O2 Arena Hotel (InterContinental London - The O2)","error":"failed to save hotel_details: context canceled"}
{"time":"2025-12-08T17:22:29.134661Z","level":"INFO","msg":"Saved hotels from unified response","hotel_count":3}
{"time":"2025-12-08T17:22:29.235272Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:22:29.235336Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:22:29.235349Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"6a9f6a9a-ba32-4a5c-9472-9ace729fda43","type":"complete","error":""}
{"time":"2025-12-08T17:23:20.78382Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"activities","cache_key":"a88eee3431f9279abb64d69ea68680fd_activities"}
{"time":"2025-12-08T17:23:20.783901Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"activities","cache_key":"a88eee3431f9279abb64d69ea68680fd_activities","prompt_length":1163}
2025/12/08 17:23:20 INFO Cache key provided but currently ignored in direct implementation cacheKey=a88eee3431f9279abb64d69ea68680fd_activities
{"time":"2025-12-08T17:23:21.202313Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:23:21.505799Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":2,"chunk_length":33,"chunk_preview":" coordinates (0.0000, 0.0000) are"}
{"time":"2025-12-08T17:23:21.807348Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":3,"chunk_length":230,"chunk_preview":" located in the middle of the Atlantic Ocean, specifically near the intersection of the Prime Meridi"}
{"time":"2025-12-08T17:23:30.293328Z","level":"INFO","msg":"LLM streaming completed","part_type":"activities","total_chunks":30,"total_response_length":5044}
{"time":"2025-12-08T17:23:30.293399Z","level":"INFO","msg":"Saved LLM response to cache","part_type":"activities","cache_key":"a88eee3431f9279abb64d69ea68680fd_activities","response_length":5044}
{"time":"2025-12-08T17:23:30.293439Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:23:30.294112Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":5}
{"time":"2025-12-08T17:23:30.362659Z","level":"INFO","msg":"Found existing city","city":"London","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd"}
{"time":"2025-12-08T17:23:30.407234Z","level":"INFO","msg":"Successfully saved interaction","interaction_id":"9b156f79-0940-4177-9777-9b395757173a"}
{"time":"2025-12-08T17:23:30.736128Z","level":"INFO","msg":"Successfully saved initial itinerary to session","poi_count":5,"top_level_pois":5}
{"time":"2025-12-08T17:23:30.736288Z","level":"INFO","msg":"Processing unified response for POI extraction","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd","response_parts":1}
{"time":"2025-12-08T17:23:30.736329Z","level":"INFO","msg":"Processing activities POIs from unified response","content_length":5044}
{"time":"2025-12-08T17:23:30.736525Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:23:30.837356Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:23:30.83747Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"0efa39f1-dd76-4132-9880-620f922cd75a","type":"complete","error":""}
{"time":"2025-12-08T17:23:30.837485Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:23:31.706863Z","level":"INFO","msg":"Cache miss for LLM response","part_type":"activities","cache_key":"6eaed74a907548d71af227731cf618c0_activities"}
{"time":"2025-12-08T17:23:31.70696Z","level":"INFO","msg":"Calling LLM for streaming","part_type":"activities","cache_key":"6eaed74a907548d71af227731cf618c0_activities","prompt_length":1163}
2025/12/08 17:23:31 INFO Cache key provided but currently ignored in direct implementation cacheKey=6eaed74a907548d71af227731cf618c0_activities
{"time":"2025-12-08T17:23:32.30589Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":1,"chunk_length":3,"chunk_preview":"The"}
{"time":"2025-12-08T17:23:32.608865Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":2,"chunk_length":33,"chunk_preview":" coordinates (0.0000, 0.0000) are"}
{"time":"2025-12-08T17:23:32.911553Z","level":"INFO","msg":"Received chunk from LLM","part_type":"activities","chunk_number":3,"chunk_length":254,"chunk_preview":" located in the middle of the Atlantic Ocean, specifically near the intersection of the Prime Meridi"}
{"time":"2025-12-08T17:23:40.189052Z","level":"INFO","msg":"LLM streaming completed","part_type":"activities","total_chunks":26,"total_response_length":4275}
{"time":"2025-12-08T17:23:40.189235Z","level":"INFO","msg":"Saved LLM response to cache","part_type":"activities","cache_key":"6eaed74a907548d71af227731cf618c0_activities","response_length":4275}
{"time":"2025-12-08T17:23:40.189271Z","level":"INFO","msg":"All streaming workers completed"}
{"time":"2025-12-08T17:23:40.189987Z","level":"INFO","msg":"Consolidated and deduplicated POIs","total_unique_pois":5}
{"time":"2025-12-08T17:23:40.196916Z","level":"INFO","msg":"Found existing city","city":"London","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd"}
{"time":"2025-12-08T17:23:40.240683Z","level":"INFO","msg":"Successfully saved interaction","interaction_id":"0bd5b289-47ee-416b-b264-4cb85d829587"}
{"time":"2025-12-08T17:23:40.556461Z","level":"INFO","msg":"Successfully saved initial itinerary to session","poi_count":5,"top_level_pois":5}
{"time":"2025-12-08T17:23:40.55656Z","level":"INFO","msg":"Processing unified response for POI extraction","city_id":"1793beb1-81fc-4b13-b2fd-13b461373afd","response_parts":1}
{"time":"2025-12-08T17:23:40.556601Z","level":"INFO","msg":"Processing activities POIs from unified response","content_length":4275}
{"time":"2025-12-08T17:23:40.556742Z","level":"INFO","msg":"Stream completed","event_type":"complete"}
{"time":"2025-12-08T17:23:40.656751Z","level":"WARN","msg":"Context cancelled, not sending stream event","eventType":"complete"}
{"time":"2025-12-08T17:23:40.656809Z","level":"INFO","msg":"Completion processing finished, event channel will be closed by handler"}
{"time":"2025-12-08T17:23:40.656824Z","level":"WARN","msg":"stream event routed to dead letter queue","event_id":"20069d35-892d-453e-beea-9cc185a589cb","type":"complete","error":""}

The issue could be on the client and on the server but I dunno what could be after all the changes I did. 
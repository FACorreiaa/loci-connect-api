Im migrating an old REST API to gRPC using Connect-Go.
I have already migrated the authentication on the folder Auth to RPC using Connect-Go.
Now I want to migrate a new the chat feature. But before we do that there is one issue:
- Chat issue has a lot of dependencies on the old REST API services.
- After importing everything I need from the old REST API services, I still have a lot of code that uses the types.
- I added the types to a type folder to make it easy
- All necessary proto schemas are on folder gen.

We first need:

1. read the chat proto gen inside the gen folder
2. understand how the chat feature is working with the old REST API (chat_handler.go.old)
3. migrate the chat feature to Connect-Go using the same pattern as the Auth folder (folder structure, service organisation)
4. make sure everything is working by running the tests on the chat folder
5. organise the chat folder and delete the import cycle
6. based on chat_handler.go.old, create the equivalent chat_handler.go using Connect-Go
7. update the routes to use the new chat handler
8. create tests for the new chat handler if there are none
9. run all tests to make sure everything is working
10. run make generate and everything should be working without import cycles
11. We also need to build the profile (profiles) handlers based on the same pattern after chat is done.
12. The client needs this handler (GetUserProfileByID) to be able to connect to the chat service and to the whole app.
13. the client is using the old http://localhost:8080/user/search-profile/default and will need this working to update the new endpoint

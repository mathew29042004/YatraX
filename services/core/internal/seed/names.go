package seed

// Synthetic Indian names grouped by language region. Combinations are random,
// so no generated person corresponds to a real one.
type namePool struct {
	Male, Female, Surname []string
}

var hindiNames = namePool{
	Male:    []string{"Aarav", "Rohit", "Amit", "Rahul", "Vikram", "Sanjay", "Rajesh", "Anil", "Suresh", "Manoj", "Deepak", "Arjun", "Kunal", "Nikhil", "Saurabh", "Ankit", "Vivek", "Gaurav", "Harsh", "Ravi", "Pankaj", "Ashok", "Mohit", "Yash", "Aditya", "Ramesh", "Vinod", "Alok"},
	Female:  []string{"Priya", "Neha", "Pooja", "Anjali", "Sunita", "Kavita", "Ritu", "Shalini", "Swati", "Aarti", "Meena", "Rekha", "Nisha", "Divya", "Shreya", "Komal", "Sonal", "Kiran", "Anita", "Savita", "Isha", "Ananya", "Geeta", "Usha"},
	Surname: []string{"Sharma", "Verma", "Gupta", "Singh", "Yadav", "Mishra", "Tiwari", "Pandey", "Chauhan", "Agarwal", "Srivastava", "Shukla", "Dubey", "Tripathi", "Saxena", "Rastogi", "Kumar", "Jha", "Thakur", "Rawat", "Bisht", "Joshi", "Meena", "Rathore"},
}

var namesByLang = map[string]namePool{
	"hi": hindiNames,
	"ur": {
		Male:    []string{"Imran", "Faisal", "Aamir", "Sajid", "Irfan", "Tariq", "Bilal", "Zubair", "Arif", "Nadeem"},
		Female:  []string{"Ayesha", "Farah", "Nazia", "Rukhsar", "Sana", "Zoya", "Shabnam", "Mehreen", "Hina"},
		Surname: []string{"Bhat", "Lone", "Dar", "Wani", "Mir", "Shah", "Khan", "Qureshi", "Malik", "Sheikh"},
	},
	"ta": {
		Male:    []string{"Arun", "Karthik", "Senthil", "Murugan", "Vignesh", "Saravanan", "Prakash", "Balaji", "Dinesh", "Ganesh", "Ramesh", "Selvam", "Manikandan", "Sathish", "Hari", "Siva", "Vijay", "Gokul", "Ilango", "Thiru"},
		Female:  []string{"Lakshmi", "Meenakshi", "Kavya", "Divya", "Priya", "Revathi", "Saranya", "Deepa", "Anitha", "Gayathri", "Nandhini", "Sangeetha", "Vasuki", "Kalaivani", "Malathi", "Tamilselvi"},
		Surname: []string{"Subramanian", "Krishnan", "Raman", "Natarajan", "Venkatesan", "Murugesan", "Rajendran", "Sundaram", "Balasubramanian", "Ramasamy", "Pandian", "Annamalai", "Chandrasekaran", "Kannan", "Arumugam"},
	},
	"ml": {
		Male:    []string{"Arjun", "Anoop", "Rahul", "Vishnu", "Abhilash", "Sreejith", "Jithin", "Akhil", "Midhun", "Faisal", "Shibu", "Joseph", "Thomas", "Ajith", "Sanju", "Biju", "Manu", "Sajan"},
		Female:  []string{"Anjali", "Aswathy", "Lakshmi", "Reshma", "Sreelakshmi", "Divya", "Athira", "Neethu", "Anu", "Meera", "Fathima", "Sneha", "Ammu", "Gopika", "Mariya", "Sruthi"},
		Surname: []string{"Nair", "Menon", "Pillai", "Kurup", "Varghese", "Thomas", "Kurian", "Mathew", "Namboothiri", "Panicker", "Warrier", "Nambiar", "Joseph", "Ali", "Haneefa", "George"},
	},
	"te": {
		Male:    []string{"Venkat", "Srinivas", "Ravi", "Kiran", "Naveen", "Praveen", "Suresh", "Mahesh", "Sai", "Chaitanya", "Harish", "Ramana", "Anil", "Teja", "Vamsi", "Raghav"},
		Female:  []string{"Lakshmi", "Sravani", "Swathi", "Keerthi", "Bhavani", "Padma", "Sirisha", "Madhavi", "Haritha", "Sindhu", "Anusha", "Deepika", "Lavanya", "Sowjanya"},
		Surname: []string{"Reddy", "Rao", "Naidu", "Chowdary", "Varma", "Raju", "Goud", "Prasad", "Sastry", "Murthy", "Yadav", "Babu"},
	},
	"kn": {
		Male:    []string{"Manjunath", "Raghu", "Prakash", "Santosh", "Shivakumar", "Naveen", "Darshan", "Girish", "Kiran", "Sudeep", "Nagaraj", "Basavaraj", "Vinay", "Chetan"},
		Female:  []string{"Kavya", "Deepa", "Rashmi", "Shwetha", "Pallavi", "Sowmya", "Bhavya", "Ramya", "Anitha", "Spoorthi", "Shruthi", "Vidya"},
		Surname: []string{"Gowda", "Hegde", "Shetty", "Rao", "Patil", "Kulkarni", "Bhat", "Naik", "Shenoy", "Kamath", "Murthy", "Desai"},
	},
	"mr": {
		Male:    []string{"Rohan", "Sachin", "Amol", "Sagar", "Nilesh", "Prashant", "Tushar", "Omkar", "Mahesh", "Ganesh", "Sandeep", "Swapnil", "Aniket", "Vaibhav"},
		Female:  []string{"Snehal", "Pooja", "Ashwini", "Priyanka", "Madhuri", "Sayali", "Gauri", "Rupali", "Manasi", "Sonali", "Rutuja", "Shital"},
		Surname: []string{"Patil", "Deshmukh", "Jadhav", "Pawar", "Kulkarni", "Joshi", "Shinde", "Gaikwad", "More", "Chavan", "Kale", "Bhosale", "Deshpande", "Sawant"},
	},
	"kok": {
		Male:    []string{"Rohan", "Savio", "Prasad", "Clinton", "Sanket", "Joaquim", "Nilesh", "Dattaprasad"},
		Female:  []string{"Sneha", "Maria", "Pranali", "Shweta", "Anisha", "Lorna", "Siddhi"},
		Surname: []string{"Naik", "Kamat", "D'Souza", "Fernandes", "Gaonkar", "Prabhu", "Sawant", "Rodrigues", "Shetye"},
	},
	"gu": {
		Male:    []string{"Hardik", "Jignesh", "Bhavesh", "Ketan", "Nirav", "Parth", "Chirag", "Mehul", "Dhaval", "Rakesh", "Hitesh", "Kalpesh"},
		Female:  []string{"Hetal", "Krupa", "Nisha", "Bhavna", "Dhara", "Jinal", "Komal", "Payal", "Riddhi", "Falguni", "Hiral"},
		Surname: []string{"Patel", "Shah", "Mehta", "Desai", "Joshi", "Trivedi", "Parikh", "Modi", "Bhatt", "Solanki", "Chauhan", "Vyas"},
	},
	"bn": {
		Male:    []string{"Arijit", "Soumya", "Debashish", "Subhajit", "Abhijit", "Sourav", "Partha", "Ranjan", "Sayan", "Anirban", "Tapas", "Indranil"},
		Female:  []string{"Moumita", "Riya", "Sutapa", "Payel", "Ananya", "Tanushree", "Sreya", "Rituparna", "Mousumi", "Sharmistha", "Debolina"},
		Surname: []string{"Banerjee", "Chatterjee", "Mukherjee", "Ghosh", "Bose", "Das", "Dutta", "Sen", "Roy", "Chakraborty", "Bhattacharya", "Sarkar", "Mondal"},
	},
	"or": {
		Male:    []string{"Subrat", "Bikash", "Debasis", "Manas", "Pradeep", "Satya", "Sarat", "Jagannath", "Ashutosh"},
		Female:  []string{"Sasmita", "Lipsa", "Smita", "Sunita", "Pragyan", "Itishree", "Mamata", "Subhashree"},
		Surname: []string{"Mohanty", "Panda", "Patnaik", "Sahoo", "Mishra", "Behera", "Nayak", "Das", "Rath", "Swain", "Pradhan"},
	},
	"pa": {
		Male:    []string{"Gurpreet", "Harpreet", "Manpreet", "Jaspreet", "Amandeep", "Navjot", "Sukhwinder", "Baljit", "Ranjit", "Kuldeep"},
		Female:  []string{"Simran", "Harleen", "Navneet", "Gurleen", "Jasleen", "Manjit", "Rajwinder", "Amrit"},
		Surname: []string{"Singh", "Gill", "Sandhu", "Dhillon", "Sidhu", "Grewal", "Bajwa", "Brar", "Randhawa"},
	},
	"as": {
		Male:    []string{"Bhaskar", "Pranjal", "Dhruba", "Hemanta", "Bikram", "Jyotirmoy", "Kaushik", "Nayan"},
		Female:  []string{"Jahnavi", "Pallabi", "Moonmoon", "Barnali", "Junmoni", "Rimjhim", "Ankita"},
		Surname: []string{"Baruah", "Bora", "Gogoi", "Saikia", "Hazarika", "Kalita", "Das", "Deka", "Sarma", "Phukan"},
	},
	"en": {
		Male:    []string{"Temjen", "Kevi", "Lanu", "Tapan", "Neiphiu", "Toshi", "Imna"},
		Female:  []string{"Akumla", "Neilo", "Vibeituonuo", "Imla", "Sentila", "Atsi"},
		Surname: []string{"Ao", "Jamir", "Rio", "Longkumer", "Sangtam", "Chang", "Tayeng", "Pertin"},
	},
}

var vendorNames = []string{
	"Annapoorna Foods", "Rail Rasoi", "Biryani House", "Udupi Upahar", "Sri Krishna Bhavan", "Punjab Da Dhaba",
	"Bengal Sweets & Snacks", "Comfort Meals", "Tiffin Express", "Saffron Kitchen", "Amma's Kitchen",
	"Highway Thali", "Mysore Café", "Chettinad Corner", "Malabar Spice", "Gujarati Thali Ghar", "Litti Chokha Point",
}

var cuisinesByLang = map[string][]string{
	"ta": {"South Indian", "Chettinad", "Biryani"}, "ml": {"Kerala", "Biryani", "Seafood"},
	"te": {"Andhra", "Biryani", "South Indian"}, "kn": {"Udupi", "South Indian", "Biryani"},
	"mr": {"Maharashtrian", "Snacks", "North Indian"}, "kok": {"Goan", "Seafood", "Snacks"},
	"gu": {"Gujarati", "Jain", "Snacks"}, "bn": {"Bengali", "Sweets", "Fish"}, "or": {"Odia", "Sweets", "North Indian"},
	"pa": {"Punjabi", "North Indian", "Tandoor"}, "as": {"Assamese", "North Indian", "Snacks"},
	"hi": {"North Indian", "Chaat", "Jain"}, "ur": {"Kashmiri", "Wazwan", "North Indian"}, "en": {"North East", "Snacks", "North Indian"},
}
